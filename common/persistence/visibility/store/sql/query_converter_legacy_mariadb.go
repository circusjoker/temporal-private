package sql

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/temporalio/sqlparser"
	"go.temporal.io/server/chasm"
	"go.temporal.io/server/common/namespace"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mariadb"
	"go.temporal.io/server/common/persistence/visibility/store/query"
	"go.temporal.io/server/common/searchattribute"
	"go.temporal.io/server/common/searchattribute/sadefs"
)

type (
	// jsonContainsExpr stands in for MySQL's `<value> member of (<json array>)`, which MariaDB
	// does not implement. NULL semantics match: json_contains(NULL, ...) is NULL, just as
	// `<value> member of (NULL)` is.
	jsonContainsExpr struct {
		sqlparser.Expr
		JSONDoc   sqlparser.Expr
		Candidate sqlparser.Expr
	}

	mariadbQueryConverter struct{}
)

var _ sqlparser.Expr = (*jsonContainsExpr)(nil)

var _ pluginQueryConverterLegacy = (*mariadbQueryConverter)(nil)

func (node *jsonContainsExpr) Format(buf *sqlparser.TrackedBuffer) {
	buf.Myprintf("json_contains(%v, json_quote(%v))", node.JSONDoc, node.Candidate)
}

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
		&mariadbQueryConverter{},
		namespaceName,
		namespaceID,
		saTypeMap,
		saMapper,
		queryString,
		chasmMapper,
		archetypeID,
	)
}

func (c *mariadbQueryConverter) getDatetimeFormat() string {
	return "2006-01-02 15:04:05.999999"
}

// getCoalesceCloseTimeExpr returns the expression open and closed workflows are sorted and
// paginated by. Unlike MySQL, which evaluates the coalesce inline, MariaDB reads the
// pre-materialized generated column so the query can use an index.
func (c *mariadbQueryConverter) getCoalesceCloseTimeExpr() sqlparser.Expr {
	return newColName(mariadb.CloseTimeOrMaxColumnName)
}

func (c *mariadbQueryConverter) convertKeywordListComparisonExpr(
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
		newExpr = &jsonContainsExpr{
			JSONDoc:   expr.Left,
			Candidate: expr.Right,
		}
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

// convertToJsonOverlapsExpr builds `json_overlaps(<col>, '[...]')`. MariaDB has no JSON type, so
// unlike MySQL the candidate array is passed as a plain string literal rather than
// `cast(... as json)`, which MariaDB rejects as a syntax error.
func (c *mariadbQueryConverter) convertToJsonOverlapsExpr(
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
	return &jsonOverlapsExpr{
		JSONDoc1: expr.Left,
		JSONDoc2: newUnsafeSQLString(string(jsonValue)),
	}, nil
}

func (c *mariadbQueryConverter) convertTextComparisonExpr(
	expr *sqlparser.ComparisonExpr,
) (sqlparser.Expr, error) {
	if !isSupportedTextOperator(expr.Operator) {
		return nil, query.NewConverterError(
			"%s: operator '%s' not supported for Text type search attribute in `%s`",
			query.InvalidExpressionErrMessage,
			expr.Operator,
			formatComparisonExprStringForError(*expr),
		)
	}
	// build the following expression:
	// `match ({expr.Left}) against ({expr.Right} in natural language mode)`
	var newExpr sqlparser.Expr = &sqlparser.MatchExpr{
		Columns: []sqlparser.SelectExpr{&sqlparser.AliasedExpr{Expr: expr.Left}},
		Expr:    expr.Right,
		Option:  sqlparser.NaturalLanguageModeStr,
	}
	if expr.Operator == sqlparser.NotEqualStr {
		newExpr = &sqlparser.NotExpr{Expr: newExpr}
	}
	return newExpr, nil
}

func (c *mariadbQueryConverter) buildSelectStmt(
	namespaceID namespace.ID,
	queryString string,
	pageSize int,
	token *pageTokenLegacy,
) (string, []any) {
	var whereClauses []string
	var queryArgs []any

	whereClauses = append(
		whereClauses,
		fmt.Sprintf("%s = ?", sadefs.GetSqlDbColName(sadefs.NamespaceID)),
	)
	queryArgs = append(queryArgs, namespaceID.String())

	if len(queryString) > 0 {
		whereClauses = append(whereClauses, queryString)
	}

	if token != nil {
		whereClauses = append(
			whereClauses,
			fmt.Sprintf(
				"((%s = ? AND %s = ? AND %s > ?) OR (%s = ? AND %s < ?) OR %s < ?)",
				sqlparser.String(c.getCoalesceCloseTimeExpr()),
				sadefs.GetSqlDbColName(sadefs.StartTime),
				sadefs.GetSqlDbColName(sadefs.RunID),
				sqlparser.String(c.getCoalesceCloseTimeExpr()),
				sadefs.GetSqlDbColName(sadefs.StartTime),
				sqlparser.String(c.getCoalesceCloseTimeExpr()),
			),
		)
		queryArgs = append(
			queryArgs,
			token.CloseTime,
			token.StartTime,
			token.RunID,
			token.CloseTime,
			token.StartTime,
			token.CloseTime,
		)
	}

	queryArgs = append(queryArgs, pageSize)

	return fmt.Sprintf(
		`SELECT %s
		FROM executions_visibility ev
		LEFT JOIN custom_search_attributes USING (%s, %s)
		LEFT JOIN chasm_search_attributes USING (%s, %s)
		WHERE %s
		ORDER BY %s DESC, %s DESC, %s
		LIMIT ?`,
		strings.Join(addPrefix("ev.", sqlplugin.DbFields), ", "),
		sadefs.GetSqlDbColName(sadefs.NamespaceID),
		sadefs.GetSqlDbColName(sadefs.RunID),
		sadefs.GetSqlDbColName(sadefs.NamespaceID),
		sadefs.GetSqlDbColName(sadefs.RunID),
		strings.Join(whereClauses, " AND "),
		sqlparser.String(c.getCoalesceCloseTimeExpr()),
		sadefs.GetSqlDbColName(sadefs.StartTime),
		sadefs.GetSqlDbColName(sadefs.RunID),
	), queryArgs
}

func (c *mariadbQueryConverter) buildCountStmt(
	namespaceID namespace.ID,
	queryString string,
	groupBy []string,
) (string, []any) {
	var whereClauses []string
	var queryArgs []any

	whereClauses = append(
		whereClauses,
		fmt.Sprintf("(%s = ?)", sadefs.GetSqlDbColName(sadefs.NamespaceID)),
	)
	queryArgs = append(queryArgs, namespaceID.String())

	if len(queryString) > 0 {
		whereClauses = append(whereClauses, queryString)
	}

	groupByClause := ""
	if len(groupBy) > 0 {
		groupByClause = fmt.Sprintf("GROUP BY %s", strings.Join(groupBy, ", "))
	}

	return fmt.Sprintf(
		`SELECT %s
		FROM executions_visibility ev
		LEFT JOIN custom_search_attributes USING (%s, %s)
		LEFT JOIN chasm_search_attributes USING (%s, %s)
		WHERE %s
		%s`,
		strings.Join(append(groupBy, "COUNT(*)"), ", "),
		sadefs.GetSqlDbColName(sadefs.NamespaceID),
		sadefs.GetSqlDbColName(sadefs.RunID),
		sadefs.GetSqlDbColName(sadefs.NamespaceID),
		sadefs.GetSqlDbColName(sadefs.RunID),
		strings.Join(whereClauses, " AND "),
		groupByClause,
	), queryArgs
}
