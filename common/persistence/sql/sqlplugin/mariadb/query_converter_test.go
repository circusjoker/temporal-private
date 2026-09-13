package mariadb

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/temporalio/sqlparser"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	"go.temporal.io/server/common/persistence/visibility/store/query"
)

func newMariaDBQueryConverter() sqlplugin.VisibilityQueryConverter {
	return mysql.NewQueryConverter(dialect{})
}

func TestMariaDBQueryConverter_GetCoalesceCloseTimeExpr(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	// MariaDB has no expression indexes: the COALESCE is a generated column.
	r.Equal(
		"close_time_or_max",
		sqlparser.String(dialect{}.GetCoalesceCloseTimeExpr()),
	)
}

func TestMariaDBQueryConverter_ConvertKeywordListComparisonExpr(t *testing.T) {
	t.Parallel()

	const sideTable = "ev.run_id in (select kl.run_id from keyword_list_search_attributes kl " +
		"where kl.namespace_id = ev.namespace_id and kl.attr = "

	keywordListCol := query.NewSAColumn(
		"AliasForKeywordList01",
		"KeywordList01",
		enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST,
	)
	// A column that is not one of the indexed keyword lists, so it must never
	// reach the side table.
	unindexedCol := query.NewSAColumn(
		"AliasForSomethingElse",
		"NotAKeywordListColumn",
		enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST,
	)

	testCases := []struct {
		name     string
		operator string
		col      *query.SAColumn
		value    sqlparser.Expr
		out      string
		err      string
	}{
		{
			// MariaDB has no `member of`, and the side-table lookup rides along
			// so the optimizer can choose. See ConvertKeywordListComparisonExpr.
			name:     "equal: json predicate AND side-table lookup",
			operator: sqlparser.EqualStr,
			col:      keywordListCol,
			value:    query.NewUnsafeSQLString("foo"),
			out: "(json_contains(KeywordList01, json_quote('foo')) and " +
				sideTable + "'KeywordList01' and kl.value = 'foo'))",
		},
		{
			// `not (json and side)` is not the negation we want, and a NOT
			// cannot use the index anyway.
			name:     "not equal: json predicate only",
			operator: sqlparser.NotEqualStr,
			col:      keywordListCol,
			value:    query.NewUnsafeSQLString("foo"),
			out:      "not json_contains(KeywordList01, json_quote('foo'))",
		},
		{
			// MariaDB has json_overlaps but no cast(... as json).
			name:     "in: json predicate AND side-table lookup",
			operator: sqlparser.InStr,
			col:      keywordListCol,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				query.NewUnsafeSQLString("bar"),
			},
			out: `(json_overlaps(KeywordList01, '["foo","bar"]') and ` +
				sideTable + "'KeywordList01' and kl.value in ('foo', 'bar')))",
		},
		{
			name:     "not in: json predicate only",
			operator: sqlparser.NotInStr,
			col:      keywordListCol,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				query.NewUnsafeSQLString("bar"),
			},
			out: `not json_overlaps(KeywordList01, '["foo","bar"]')`,
		},
		{
			// The side table cannot hold a value this long, so it must not be
			// consulted for one -- the answer would be a false negative.
			name:     "value too long for the index: json predicate only",
			operator: sqlparser.EqualStr,
			col:      keywordListCol,
			value:    query.NewUnsafeSQLString(strings.Repeat("x", 256)),
			out:      "json_contains(KeywordList01, json_quote('" + strings.Repeat("x", 256) + "'))",
		},
		{
			name:     "in with one value too long: json predicate only",
			operator: sqlparser.InStr,
			col:      keywordListCol,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				query.NewUnsafeSQLString(strings.Repeat("x", 256)),
			},
			out: `json_overlaps(KeywordList01, '["foo","` + strings.Repeat("x", 256) + `"]')`,
		},
		{
			name:     "column not indexed by the side table: json predicate only",
			operator: sqlparser.EqualStr,
			col:      unindexedCol,
			value:    query.NewUnsafeSQLString("foo"),
			out:      "json_contains(NotAKeywordListColumn, json_quote('foo'))",
		},
		{
			name:     "invalid in expression",
			operator: sqlparser.InStr,
			col:      keywordListCol,
			value: sqlparser.ValTuple{
				query.NewUnsafeSQLString("foo"),
				sqlparser.NewIntVal([]byte("123")),
			},
			err: query.InvalidExpressionErrMessage,
		},
		{
			name:     "invalid operator",
			operator: sqlparser.LessThanStr,
			col:      keywordListCol,
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
			out, err := dialect{}.
				ConvertKeywordListComparisonExpr(tc.operator, tc.col, tc.value)
			if tc.err != "" {
				r.Error(err)
				r.ErrorContains(err, tc.err)
				var expectedErr *query.ConverterError
				r.ErrorAs(err, &expectedErr)
				return
			}
			r.NoError(err)
			r.Equal(tc.out, sqlparser.String(out))
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
