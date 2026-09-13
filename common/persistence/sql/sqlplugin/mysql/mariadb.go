package mysql

import (
	"encoding/json"

	"github.com/temporalio/sqlparser"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/visibility/store/query"
)

// PluginNameMariaDB is the name of the MariaDB plugin.
//
// MariaDB speaks the MySQL wire protocol and accepts the whole execution
// (non-visibility) schema unchanged, so it reuses this package rather than
// duplicating it -- the same way the postgresql package hosts both the pq and
// pgx plugins. The differences are confined to:
//
//   - the visibility schema (schema/mariadb/v11/visibility), because MariaDB
//     11.4 has no expression indexes, no multi-valued (ARRAY) indexes and no
//     `->` / `->>` JSON operators; and
//   - the KeywordList and close-time SQL emitted by the query converter below.
//
// Verified against mariadb:11.4.
const PluginNameMariaDB = "mariadb"

func init() {
	sql.RegisterPlugin(PluginNameMariaDB, &plugin{
		flavor:         mariaDBFlavor,
		queryConverter: &queryConverter{mariaDBDialect{}},
	})
}

type mariaDBDialect struct{}

var _ visibilityDialect = (*mariaDBDialect)(nil)

// closeTimeOrMaxColumn is the generated column that materializes the
// COALESCE(close_time, <max datetime>) that MySQL writes inline in each index.
// MariaDB has no expression indexes, so the expression has to be a real column
// for the visibility indexes to be usable.
var closeTimeOrMaxColumn = query.NewColName("close_time_or_max")

func (mariaDBDialect) GetCoalesceCloseTimeExpr() sqlparser.Expr {
	return closeTimeOrMaxColumn
}

// ConvertKeywordListComparisonExpr builds MariaDB's equivalents of MySQL's
// `x member of (col)` (which MariaDB does not have) and of `json_overlaps` with
// a `cast(... as json)` argument (MariaDB has json_overlaps but no cast to json).
func (d mariaDBDialect) ConvertKeywordListComparisonExpr(
	operator string,
	col *query.SAColumn,
	value sqlparser.Expr,
) (sqlparser.Expr, error) {
	var negate bool
	var newExpr sqlparser.Expr
	switch operator {
	case sqlparser.EqualStr, sqlparser.NotEqualStr:
		newExpr = &jsonContainsExpr{JSONDoc: col, Candidate: value}
		negate = operator == sqlparser.NotEqualStr
	case sqlparser.InStr, sqlparser.NotInStr:
		var err error
		newExpr, err = d.buildJSONOverlapsExpr(col, value)
		if err != nil {
			return nil, err
		}
		negate = operator == sqlparser.NotInStr
	default:
		// this should never happen since isSupportedKeywordListOperator should already fail
		return nil, query.NewConverterError(
			"%s: operator '%s' not supported for KeywordList type",
			query.InvalidExpressionErrMessage,
			operator,
		)
	}

	if negate {
		newExpr = &sqlparser.NotExpr{Expr: newExpr}
	}
	return newExpr, nil
}

func (mariaDBDialect) buildJSONOverlapsExpr(
	col *query.SAColumn,
	value sqlparser.Expr,
) (*jsonOverlapsExpr, error) {
	valTuple, isValTuple := value.(sqlparser.ValTuple)
	if !isValTuple {
		return nil, query.NewConverterError(
			"%s: unexpected value type (expected tuple of strings, got %s)",
			query.InvalidExpressionErrMessage,
			sqlparser.String(value),
		)
	}
	values, err := query.GetUnsafeStringTupleValues(valTuple)
	if err != nil {
		return nil, err
	}
	jsonValue, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	// Unlike MySQL, the JSON document is passed as a plain string literal:
	// MariaDB has no CAST(... AS JSON).
	return &jsonOverlapsExpr{
		JSONDoc1: col,
		JSONDoc2: query.NewUnsafeSQLString(string(jsonValue)),
	}, nil
}

// jsonContainsExpr renders `json_contains(<doc>, json_quote(<candidate>))`,
// MariaDB's stand-in for MySQL's `<candidate> member of (<doc>)`.
type jsonContainsExpr struct {
	sqlparser.Expr
	JSONDoc   sqlparser.Expr
	Candidate sqlparser.Expr
}

var _ sqlparser.Expr = (*jsonContainsExpr)(nil)

func (node *jsonContainsExpr) Format(buf *sqlparser.TrackedBuffer) {
	buf.Myprintf("json_contains(%v, json_quote(%v))", node.JSONDoc, node.Candidate)
}
