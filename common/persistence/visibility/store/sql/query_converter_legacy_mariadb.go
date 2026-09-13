package sql

import (
	"encoding/json"

	"github.com/temporalio/sqlparser"
	"go.temporal.io/server/chasm"
	"go.temporal.io/server/common/namespace"
	"go.temporal.io/server/common/persistence/visibility/store/query"
	"go.temporal.io/server/common/searchattribute"
)

// mariaDBDialect is the MariaDB half of the MySQL-family legacy query converter.
// See sqlplugin/mysql/mariadb.go for why MariaDB needs its own SQL here:
// it has no expression indexes, no `member of` operator and no cast to json.
type mariaDBDialect struct{}

var _ legacyVisibilityDialect = (*mariaDBDialect)(nil)

// closeTimeOrMaxColName is the generated column that materializes
// COALESCE(close_time, <max datetime>) in the MariaDB visibility schema.
const closeTimeOrMaxColName = "close_time_or_max"

func newMariaDBQueryConverter(
	namespaceName namespace.Name,
	namespaceID namespace.ID,
	saTypeMap searchattribute.NameTypeMap,
	saMapper searchattribute.Mapper,
	queryString string,
	chasmMapper *chasm.VisibilitySearchAttributesMapper,
	archetypeID chasm.ArchetypeID,
) *QueryConverterLegacy {
	return newQueryConverterInternal(
		&mysqlQueryConverter{mariaDBDialect{}},
		namespaceName,
		namespaceID,
		saTypeMap,
		saMapper,
		queryString,
		chasmMapper,
		archetypeID,
	)
}

func (c mariaDBDialect) getCoalesceCloseTimeExpr() sqlparser.Expr {
	return newColName(closeTimeOrMaxColName)
}

func (c mariaDBDialect) convertKeywordListComparisonExpr(
	expr *sqlparser.ComparisonExpr,
) (sqlparser.Expr, error) {
	if !isSupportedKeywordListOperator(expr.Operator) {
		return nil, query.NewConverterError(
			"%s: operator '%s' not supported for KeywordList type search attribute in `%s`",
			query.InvalidExpressionErrMessage,
			expr.Operator,
			formatComparisonExprStringForError(*expr),
		)
	}

	var negate bool
	var newExpr sqlparser.Expr
	switch expr.Operator {
	case sqlparser.EqualStr, sqlparser.NotEqualStr:
		newExpr = &jsonContainsExpr{JSONDoc: expr.Left, Candidate: expr.Right}
		negate = expr.Operator == sqlparser.NotEqualStr
	case sqlparser.InStr, sqlparser.NotInStr:
		var err error
		newExpr, err = c.convertToJsonOverlapsExpr(expr)
		if err != nil {
			return nil, err
		}
		negate = expr.Operator == sqlparser.NotInStr
	default:
		// this should never happen since isSupportedKeywordListOperator should already fail
		return nil, query.NewConverterError(
			"%s: operator '%s' not supported for KeywordList type search attribute in `%s`",
			query.InvalidExpressionErrMessage,
			expr.Operator,
			formatComparisonExprStringForError(*expr),
		)
	}

	if negate {
		newExpr = &sqlparser.NotExpr{Expr: newExpr}
	}
	return newExpr, nil
}

func (c mariaDBDialect) convertToJsonOverlapsExpr(
	expr *sqlparser.ComparisonExpr,
) (*jsonOverlapsExpr, error) {
	valTuple, isValTuple := expr.Right.(sqlparser.ValTuple)
	if !isValTuple {
		return nil, query.NewConverterError(
			"%s: unexpected value type (expected tuple of strings, got %s)",
			query.InvalidExpressionErrMessage,
			sqlparser.String(expr.Right),
		)
	}
	values, err := getUnsafeStringTupleValues(valTuple)
	if err != nil {
		return nil, err
	}
	jsonValue, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	// Unlike MySQL, the JSON document is a plain string literal: MariaDB has no
	// CAST(... AS JSON).
	return &jsonOverlapsExpr{
		JSONDoc1: expr.Left,
		JSONDoc2: newUnsafeSQLString(string(jsonValue)),
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
