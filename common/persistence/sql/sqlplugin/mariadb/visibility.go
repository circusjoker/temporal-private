package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/server/common/persistence/sql/sqlplugin"
)

// Known divergence from sqlplugin/mysql: errors from tx.Commit() are returned
// unclassified. Statement errors still pass through the shared conn, which
// converts them, but the DatabaseHandle that does the conversion is not reachable
// from here. Commit failures therefore reach callers as driver errors rather than
// serviceerrors.
//
// MariaDB owns its visibility writes rather than reusing the ones in
// sqlplugin/mysql, because it has one more table to keep: the
// keyword_list_search_attributes side table that stands in for the multi-valued
// indexes MariaDB does not support. That table has to be written in the same
// transaction as the row it indexes, and mysql's methods open and commit their
// own transaction, so there is nothing to hook into from outside.
//
// The SQL for the three shared tables is the same as MySQL's; only the extra
// table and the version guard around it are new.

var (
	templateInsertWorkflowExecution = fmt.Sprintf(
		`INSERT INTO executions_visibility (%s)
		VALUES (%s)
		ON DUPLICATE KEY UPDATE run_id = VALUES(run_id)`,
		strings.Join(sqlplugin.DbFields, ", "),
		sqlplugin.BuildNamedPlaceholder(sqlplugin.DbFields...),
	)

	templateInsertCustomSearchAttributes = `
		INSERT INTO custom_search_attributes (
			namespace_id, run_id, search_attributes
		) VALUES (:namespace_id, :run_id, :search_attributes)
		ON DUPLICATE KEY UPDATE run_id = VALUES(run_id)`

	templateInsertChasmSearchAttributes = `
		INSERT INTO chasm_search_attributes (
			namespace_id, run_id, search_attributes
		) VALUES (:namespace_id, :run_id, :search_attributes)
		ON DUPLICATE KEY UPDATE run_id = VALUES(run_id)`

	templateUpsertWorkflowExecution = fmt.Sprintf(
		`INSERT INTO executions_visibility (%s)
		VALUES (%s)
		%s`,
		strings.Join(sqlplugin.DbFields, ", "),
		sqlplugin.BuildNamedPlaceholder(sqlplugin.DbFields...),
		buildOnDuplicateKeyUpdate(sqlplugin.DbFields...),
	)

	templateUpsertCustomSearchAttributes = `
		INSERT INTO custom_search_attributes (
			namespace_id, run_id, search_attributes, _version
		) VALUES (:namespace_id, :run_id, :search_attributes, :_version)` +
		buildOnDuplicateKeyUpdate("search_attributes", sqlplugin.VersionColumnName)

	templateUpsertChasmSearchAttributes = `
		INSERT INTO chasm_search_attributes (
			namespace_id, run_id, search_attributes, _version
		) VALUES (:namespace_id, :run_id, :search_attributes, :_version)` +
		buildOnDuplicateKeyUpdate("search_attributes", sqlplugin.VersionColumnName)

	templateDeleteWorkflowExecution = `
		DELETE FROM executions_visibility
		WHERE namespace_id = :namespace_id AND run_id = :run_id`

	templateDeleteCustomSearchAttributes = `
		DELETE FROM custom_search_attributes
		WHERE namespace_id = :namespace_id AND run_id = :run_id`

	templateDeleteChasmSearchAttributes = `
		DELETE FROM chasm_search_attributes
		WHERE namespace_id = :namespace_id AND run_id = :run_id`

	// The side table is rebuilt from the search attributes actually committed to
	// each of the three visibility tables, read back inside this transaction.
	// Their upserts are version-guarded independently, so they can hold different
	// content, and each keyword list must follow the table whose generated column
	// a query reads.
	templateSelectStoredSearchAttributes = `
		SELECT ev.search_attributes AS ev_sa,
		       csa.search_attributes AS csa_sa,
		       chasm.search_attributes AS chasm_sa
		FROM executions_visibility ev
		LEFT JOIN custom_search_attributes csa
		       ON csa.namespace_id = ev.namespace_id AND csa.run_id = ev.run_id
		LEFT JOIN chasm_search_attributes chasm
		       ON chasm.namespace_id = ev.namespace_id AND chasm.run_id = ev.run_id
		WHERE ev.namespace_id = ? AND ev.run_id = ?`

	templateDeleteKeywordListRows = `
		DELETE FROM keyword_list_search_attributes
		WHERE namespace_id = ? AND run_id = ?`
)

func buildOnDuplicateKeyUpdate(fields ...string) string {
	items := make([]string, len(fields))
	for i, field := range fields {
		// This line is to ensure that no update occurs (for any column) if the version is behind the saved version.
		items[i] = fmt.Sprintf("%v = IF(%v < VALUES(%v), VALUES(%v), %v)",
			field, sqlplugin.VersionColumnName, sqlplugin.VersionColumnName, field, field)
	}
	return fmt.Sprintf(" ON DUPLICATE KEY UPDATE %s", strings.Join(items, ", "))
}

// InsertIntoVisibility inserts a row into the visibility table. If a row already
// exists it is left as is.
func (d *db) InsertIntoVisibility(
	ctx context.Context,
	row *sqlplugin.VisibilityRow,
) (sql.Result, error) {
	return d.writeVisibility(
		ctx, row,
		templateInsertWorkflowExecution,
		templateInsertCustomSearchAttributes,
		templateInsertChasmSearchAttributes,
	)
}

// ReplaceIntoVisibility replaces an existing row, or creates one if absent.
func (d *db) ReplaceIntoVisibility(
	ctx context.Context,
	row *sqlplugin.VisibilityRow,
) (sql.Result, error) {
	return d.writeVisibility(
		ctx, row,
		templateUpsertWorkflowExecution,
		templateUpsertCustomSearchAttributes,
		templateUpsertChasmSearchAttributes,
	)
}

func (d *db) writeVisibility(
	ctx context.Context,
	row *sqlplugin.VisibilityRow,
	executionTemplate string,
	customSATemplate string,
	chasmSATemplate string,
) (result sql.Result, retError error) {
	finalRow := prepareRowForDB(row)

	tx, conn, err := d.beginVisibilityTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		rollbackErr := tx.Rollback()
		// sql.ErrTxDone means the transaction already closed, so ignore it.
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retError = fmt.Errorf("transaction rollback failed: %w", retError)
		}
	}()

	result, err = conn.NamedExecContext(ctx, executionTemplate, finalRow)
	if err != nil {
		return nil, fmt.Errorf("unable to write workflow execution: %w", err)
	}
	if _, err = conn.NamedExecContext(ctx, customSATemplate, finalRow); err != nil {
		return nil, fmt.Errorf("unable to write custom search attributes: %w", err)
	}
	if _, err = conn.NamedExecContext(ctx, chasmSATemplate, finalRow); err != nil {
		return nil, fmt.Errorf("unable to write chasm search attributes: %w", err)
	}
	if err = d.writeKeywordListRows(ctx, conn, finalRow.NamespaceID, finalRow.RunID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// writeKeywordListRows rebuilds the side table for one execution.
//
// It reads back what the three visibility tables actually hold, inside this
// transaction, and derives the index from that. Deriving from the row we tried to
// write instead would need a guard replicating the upsert's exact semantics --
// and visibility upserts are version-guarded three times over, independently, so
// a write can land in one table and be rejected by another.
func (d *db) writeKeywordListRows(
	ctx context.Context,
	conn sqlplugin.Conn,
	namespaceID string,
	runID string,
) error {
	var stored storedSearchAttributes
	err := conn.GetContext(ctx, &stored, templateSelectStoredSearchAttributes, namespaceID, runID)
	if errors.Is(err, sql.ErrNoRows) {
		// No visibility row, so nothing to index.
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to read back stored search attributes: %w", err)
	}

	rows, err := buildKeywordListRows(namespaceID, runID, stored)
	if err != nil {
		return fmt.Errorf("unable to build keyword list index: %w", err)
	}

	if _, err := conn.ExecContext(ctx, templateDeleteKeywordListRows, namespaceID, runID); err != nil {
		return fmt.Errorf("unable to clear keyword list index: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// One multi-row INSERT rather than a statement per value: an execution can
	// carry dozens of build ids.
	placeholders := make([]string, len(rows))
	args := make([]any, 0, len(rows)*4)
	for i, r := range rows {
		placeholders[i] = "(?, ?, ?, ?)"
		args = append(args, r.NamespaceID, r.RunID, r.Attr, r.Value)
	}
	stmt := "INSERT INTO keyword_list_search_attributes (namespace_id, run_id, attr, value) VALUES " +
		strings.Join(placeholders, ", ")
	if _, err := conn.ExecContext(ctx, stmt, args...); err != nil {
		return fmt.Errorf("unable to write keyword list index: %w", err)
	}
	return nil
}

// DeleteFromVisibility deletes a row from the visibility table if it exists.
func (d *db) DeleteFromVisibility(
	ctx context.Context,
	filter sqlplugin.VisibilityDeleteFilter,
) (result sql.Result, retError error) {
	tx, conn, err := d.beginVisibilityTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retError = fmt.Errorf("transaction rollback failed: %w", retError)
		}
	}()

	if _, err = conn.NamedExecContext(ctx, templateDeleteCustomSearchAttributes, filter); err != nil {
		return nil, fmt.Errorf("unable to delete custom search attributes: %w", err)
	}
	result, err = conn.NamedExecContext(ctx, templateDeleteWorkflowExecution, filter)
	if err != nil {
		return nil, fmt.Errorf("unable to delete workflow execution: %w", err)
	}
	if _, err = conn.NamedExecContext(ctx, templateDeleteChasmSearchAttributes, filter); err != nil {
		return nil, fmt.Errorf("unable to delete chasm search attributes: %w", err)
	}
	if _, err = conn.ExecContext(ctx, templateDeleteKeywordListRows, filter.NamespaceID, filter.RunID); err != nil {
		return nil, fmt.Errorf("unable to delete keyword list index: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// beginVisibilityTx starts a transaction and exposes it for raw statements.
//
// sqlplugin.Tx only carries the typed CRUD methods, but the transaction handle
// sqlplugin/mysql returns is the same object that implements sqlplugin.Conn, so
// the assertion is how a dialect runs its own SQL inside a shared transaction.
func (d *db) beginVisibilityTx(ctx context.Context) (sqlplugin.Tx, sqlplugin.Conn, error) {
	tx, err := d.storeDB.BeginTx(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, ok := tx.(sqlplugin.Conn)
	if !ok {
		_ = tx.Rollback()
		return nil, nil, fmt.Errorf(
			"mariadb: transaction handle %T does not implement sqlplugin.Conn", tx)
	}
	return tx, conn, nil
}

// minDateTime mirrors the zero-time sentinel sqlplugin/mysql writes, so rows
// written by either are read back the same way.
var minDateTime = func() time.Time {
	t, err := time.Parse(time.RFC3339, "1000-01-01T00:00:00Z")
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return t.UTC()
}()

func toDBDateTime(t time.Time) time.Time {
	if t.IsZero() {
		return minDateTime
	}
	return t.UTC().Truncate(time.Microsecond)
}

func prepareRowForDB(row *sqlplugin.VisibilityRow) *sqlplugin.VisibilityRow {
	if row == nil {
		return nil
	}
	finalRow := *row
	finalRow.StartTime = toDBDateTime(finalRow.StartTime)
	finalRow.ExecutionTime = toDBDateTime(finalRow.ExecutionTime)
	if finalRow.CloseTime != nil {
		closeTime := toDBDateTime(*finalRow.CloseTime)
		finalRow.CloseTime = &closeTime
	}
	if finalRow.SearchAttributes != nil {
		saMap := make(map[string]any, len(*finalRow.SearchAttributes))
		for name, value := range *finalRow.SearchAttributes {
			if dt, ok := value.(time.Time); ok {
				saMap[name] = dt.Format(time.RFC3339Nano)
				continue
			}
			saMap[name] = value
		}
		sa := sqlplugin.VisibilitySearchAttributes(saMap)
		finalRow.SearchAttributes = &sa
	}
	return &finalRow
}
