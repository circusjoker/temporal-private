package sql

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"
	"github.com/temporalio/sqlparser"
	"go.temporal.io/server/common/persistence/visibility/store/query"
)

type (
	mariadbQueryConverterSuite struct {
		queryConverterSuite
	}
)

func TestMariaDBQueryConverterSuite(t *testing.T) {
	s := &mariadbQueryConverterSuite{
		queryConverterSuite: queryConverterSuite{
			pqc: &mariadbQueryConverter{},
		},
	}
	suite.Run(t, s)
}

func (s *mariadbQueryConverterSuite) TestGetCoalesceCloseTimeExpr() {
	expr := s.queryConverter.getCoalesceCloseTimeExpr()
	s.Equal("close_time_or_max", sqlparser.String(expr))
}

func (s *mariadbQueryConverterSuite) TestConvertKeywordListComparisonExpr() {
	var tests = []testCase{
		{
			name:   "invalid operator",
			input:  "AliasForKeywordList01 < 'foo'",
			output: "",
			err: query.NewConverterError(
				"%s: operator '%s' not supported for KeywordList type search attribute in `%s`",
				query.InvalidExpressionErrMessage,
				sqlparser.LessThanStr,
				"AliasForKeywordList01 < 'foo'",
			),
		},
		{
			name:   "valid equal expression",
			input:  "AliasForKeywordList01 = 'foo'",
			output: "json_contains(KeywordList01, json_quote('foo'))",
			err:    nil,
		},
		{
			name:   "valid not equal expression",
			input:  "AliasForKeywordList01 != 'foo'",
			output: "not json_contains(KeywordList01, json_quote('foo'))",
			err:    nil,
		},
		{
			name:   "valid in expression",
			input:  "AliasForKeywordList01 in ('foo', 'bar')",
			output: "json_overlaps(KeywordList01, '[\"foo\",\"bar\"]')",
			err:    nil,
		},
		{
			name:   "valid not in expression",
			input:  "AliasForKeywordList01 not in ('foo', 'bar')",
			output: "not json_overlaps(KeywordList01, '[\"foo\",\"bar\"]')",
			err:    nil,
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			sql := fmt.Sprintf("select * from table1 where %s", tc.input)
			stmt, err := sqlparser.Parse(sql)
			s.NoError(err)
			expr := stmt.(*sqlparser.Select).Where.Expr
			err = s.queryConverter.convertComparisonExpr(&expr)
			if tc.err == nil {
				s.NoError(err)
				s.Equal(tc.output, sqlparser.String(expr))
			} else {
				s.Error(err)
				s.Equal(err, tc.err)
			}
		})
	}
}

func (s *mariadbQueryConverterSuite) TestConvertTextComparisonExpr() {
	var tests = []testCase{
		{
			name:   "invalid operator",
			input:  "AliasForText01 < 'foo'",
			output: "",
			err: query.NewConverterError(
				"%s: operator '%s' not supported for Text type search attribute in `%s`",
				query.InvalidExpressionErrMessage,
				sqlparser.LessThanStr,
				"AliasForText01 < 'foo'",
			),
		},
		{
			name:   "valid equal expression",
			input:  "AliasForText01 = 'foo bar'",
			output: "match(Text01) against ('foo bar' in natural language mode)",
			err:    nil,
		},
		{
			name:   "valid not equal expression",
			input:  "AliasForText01 != 'foo bar'",
			output: "not match(Text01) against ('foo bar' in natural language mode)",
			err:    nil,
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			sql := fmt.Sprintf("select * from table1 where %s", tc.input)
			stmt, err := sqlparser.Parse(sql)
			s.NoError(err)
			expr := stmt.(*sqlparser.Select).Where.Expr
			err = s.queryConverter.convertComparisonExpr(&expr)
			if tc.err == nil {
				s.NoError(err)
				s.Equal(tc.output, sqlparser.String(expr))
			} else {
				s.Error(err)
				s.Equal(err, tc.err)
			}
		})
	}
}
