package mysql

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/temporalio/sqlparser"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/persistence/visibility/store/query"
)

func newMariaDBQueryConverter() *queryConverter {
	return &queryConverter{mariaDBDialect{}}
}

func TestMariaDBQueryConverter_GetCoalesceCloseTimeExpr(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	// MariaDB has no expression indexes: the COALESCE is a generated column.
	r.Equal(
		"close_time_or_max",
		sqlparser.String(newMariaDBQueryConverter().GetCoalesceCloseTimeExpr()),
	)
}

func TestMariaDBQueryConverter_ConvertKeywordListComparisonExpr(t *testing.T) {
	t.Parallel()

	keywordListCol := query.NewSAColumn(
		"AliasForKeywordList01",
		"KeywordList01",
		enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST,
	)

	testCases := []struct {
		name     string
		operator string
		value    sqlparser.Expr
		out      string
		err      string
	}{
		{
			// MariaDB has no `member of` operator.
			name:     "equal uses json_contains",
			operator: sqlparser.EqualStr,
			value:    query.NewUnsafeSQLString("foo"),
			out:      "json_contains(KeywordList01, json_quote('foo'))",
		},
		{
			name:     "not equal uses json_contains",
			operator: sqlparser.NotEqualStr,
			value:    query.NewUnsafeSQLString("foo"),
			out:      "not json_contains(KeywordList01, json_quote('foo'))",
		},
		{
			// MariaDB has json_overlaps but no cast(... as json).
			name:     "in uses json_overlaps with a string literal",
			operator: sqlparser.InStr,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				query.NewUnsafeSQLString("bar"),
			},
			out: `json_overlaps(KeywordList01, '["foo","bar"]')`,
		},
		{
			name:     "not in uses json_overlaps with a string literal",
			operator: sqlparser.NotInStr,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				query.NewUnsafeSQLString("bar"),
			},
			out: `not json_overlaps(KeywordList01, '["foo","bar"]')`,
		},
		{
			name:     "invalid in expression",
			operator: sqlparser.InStr,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				sqlparser.NewIntVal([]byte("123")),
			},
			err: query.InvalidExpressionErrMessage,
		},
		{
			name:     "invalid operator",
			operator: sqlparser.LessThanStr,
			value:    query.NewUnsafeSQLString("foo"),
			err: fmt.Sprintf(
				"%s: operator '<' not supported for KeywordList type",
				query.InvalidExpressionErrMessage,
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			out, err := newMariaDBQueryConverter().
				ConvertKeywordListComparisonExpr(tc.operator, keywordListCol, tc.value)
			if tc.err != "" {
				r.Error(err)
				r.ErrorContains(err, tc.err)
				var expectedErr *query.ConverterError
				r.ErrorAs(err, &expectedErr)
			} else {
				r.NoError(err)
				r.Equal(tc.out, sqlparser.String(out))
			}
		})
	}
}

func TestMariaDBQueryConverter_BuildSelectStmt(t *testing.T) {
	t.Parallel()

	dbFields := make([]string, len(sqlplugin.DbFields))
	for i, f := range sqlplugin.DbFields {
		dbFields[i] = "ev." + f
	}
	closeTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	r := require.New(t)
	qc := newMariaDBQueryConverter()
	qp := &query.QueryParams[sqlparser.Expr]{}

	stmt, args := qc.BuildSelectStmt(qp, 20, &sqlplugin.VisibilityPageToken{
		CloseTime: closeTime,
		StartTime: startTime,
		RunID:     "run-id",
	})

	r.Equal(
		fmt.Sprintf(
			"SELECT %s FROM executions_visibility ev "+
				"LEFT JOIN custom_search_attributes USING (namespace_id, run_id) "+
				"LEFT JOIN chasm_search_attributes USING (namespace_id, run_id) "+
				"WHERE ((close_time_or_max = ? AND start_time = ? AND run_id > ?) "+
				"OR (close_time_or_max = ? AND start_time < ?) OR close_time_or_max < ?) "+
				"ORDER BY close_time_or_max DESC, start_time DESC, run_id LIMIT ?",
			strings.Join(dbFields, ", "),
		),
		stmt,
	)
	r.Equal([]any{closeTime, startTime, "run-id", closeTime, startTime, closeTime, 20}, args)
	// The MySQL inline COALESCE must not leak into MariaDB SQL.
	r.NotContains(stmt, "coalesce")
}
