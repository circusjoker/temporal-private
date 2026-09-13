package mariadb

import (
	"encoding/json"

	"github.com/temporalio/sqlparser"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	"go.temporal.io/server/common/persistence/visibility/store/query"
)

// dialect supplies the visibility SQL MariaDB spells differently from MySQL 8.
// Everything else comes from mysql.NewQueryConverter.
type dialect struct{}

var _ mysql.VisibilityDialect = (*dialect)(nil)

// closeTimeOrMaxColumn is the generated column that materializes the
// COALESCE(close_time, <max datetime>) MySQL writes inline in each index.
// MariaDB has no expression indexes, so the expression has to be a real column
// for the visibility indexes to be usable.
var closeTimeOrMaxColumn = query.NewColName("close_time_or_max")

func (dialect) GetCoalesceCloseTimeExpr() sqlparser.Expr {
	return closeTimeOrMaxColumn
}

// ConvertKeywordListComparisonExpr resolves a KeywordList predicate.
//
// MariaDB has neither MySQL's `member of` operator nor the multi-valued indexes
// MySQL uses here, so a positive match emits *both* the JSON predicate and a
// lookup into the keyword_list_search_attributes side table, ANDed. They are
// redundant on purpose:
//
//   - The optimizer picks whichever is cheaper. Measured on 200k executions in
//     one namespace, the side table turns a highly selective match from 1273ms
//     (scanning 191k rows) into under 1ms, while a match that hits most rows
//     still resolves through the ordered index in about 1ms because ORDER BY +
//     LIMIT lets it stop early. Neither shape is fast for both on its own.
//   - The JSON predicate decides correctness, so a side-table row that should
//     not be there cannot produce a wrong answer.
//
// A negated match emits the JSON predicate alone: `NOT (json AND side)` is not
// the negation we want, and a NOT cannot use the index regardless.
//
// Values longer than the side table's VARCHAR(255) are not indexed, so a query
// for one simply omits the side-table half and scans, which is correct.
func (d dialect) ConvertKeywordListComparisonExpr(
	operator string,
	col *query.SAColumn,
	value sqlparser.Expr,
) (sqlparser.Expr, error) {
	switch operator {
	case sqlparser.EqualStr, sqlparser.NotEqualStr:
		jsonExpr := sqlparser.Expr(&jsonContainsExpr{JSONDoc: col, Candidate: value})
		if operator == sqlparser.NotEqualStr {
			return &sqlparser.NotExpr{Expr: jsonExpr}, nil
		}
		str, ok := value.(*query.UnsafeSQLString)
		if !ok || !indexable(str.Val) || !isKeywordListAttr(col.FieldName) {
			return jsonExpr, nil
		}
		return andExprs(jsonExpr, newKeywordListLookup(col.FieldName, []string{str.Val})), nil

	case sqlparser.InStr, sqlparser.NotInStr:
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
		jsonExpr, err := buildJSONOverlapsExpr(col, values)
		if err != nil {
			return nil, err
		}
		if operator == sqlparser.NotInStr {
			return &sqlparser.NotExpr{Expr: jsonExpr}, nil
		}
		if !isKeywordListAttr(col.FieldName) || !allIndexable(values) {
			return jsonExpr, nil
		}
		return andExprs(jsonExpr, newKeywordListLookup(col.FieldName, values)), nil

	default:
		// this should never happen since isSupportedKeywordListOperator should already fail
		return nil, query.NewConverterError(
			"%s: operator '%s' not supported for KeywordList type",
			query.InvalidExpressionErrMessage,
			operator,
		)
	}
}

func andExprs(left, right sqlparser.Expr) sqlparser.Expr {
	return &sqlparser.ParenExpr{Expr: &sqlparser.AndExpr{Left: left, Right: right}}
}

func indexable(v string) bool { return len(v) <= maxKeywordListValueLen }

func allIndexable(values []string) bool {
	for _, v := range values {
		if !indexable(v) {
			return false
		}
	}
	return len(values) > 0
}

func buildJSONOverlapsExpr(col *query.SAColumn, values []string) (*jsonOverlapsExpr, error) {
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

// keywordListLookup renders the semi-join into the side table. It is written as
// `run_id in (select ...)` rather than `exists (...)` because MariaDB turns the
// IN form into a semi-join driven from the indexed side table, while the
// correlated EXISTS form makes it probe once per candidate row.
type keywordListLookup struct {
	sqlparser.Expr
	Attr   string
	Values []string
}

var _ sqlparser.Expr = (*keywordListLookup)(nil)

func newKeywordListLookup(attr string, values []string) *keywordListLookup {
	return &keywordListLookup{Attr: attr, Values: values}
}

func (node *keywordListLookup) Format(buf *sqlparser.TrackedBuffer) {
	buf.Myprintf("ev.run_id in (select kl.run_id from keyword_list_search_attributes kl")
	buf.Myprintf(" where kl.namespace_id = ev.namespace_id and kl.attr = %v",
		query.NewUnsafeSQLString(node.Attr))
	if len(node.Values) == 1 {
		buf.Myprintf(" and kl.value = %v)", query.NewUnsafeSQLString(node.Values[0]))
		return
	}
	buf.Myprintf(" and kl.value in (")
	for i, v := range node.Values {
		if i > 0 {
			buf.Myprintf(", ")
		}
		buf.Myprintf("%v", query.NewUnsafeSQLString(v))
	}
	buf.Myprintf("))")
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

// jsonOverlapsExpr renders `json_overlaps(<doc1>, <doc2>)`.
type jsonOverlapsExpr struct {
	sqlparser.Expr
	JSONDoc1 sqlparser.Expr
	JSONDoc2 sqlparser.Expr
}

var _ sqlparser.Expr = (*jsonOverlapsExpr)(nil)

func (node *jsonOverlapsExpr) Format(buf *sqlparser.TrackedBuffer) {
	buf.Myprintf("json_overlaps(%v, %v)", node.JSONDoc1, node.JSONDoc2)
}
