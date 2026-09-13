package mariadb

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
)

// TestKeywordListAttrsMatchSchema fails if keywordListAttrs drifts from the JSON
// columns in the visibility schema.
//
// Drift is silent and one-directional: a keyword list added to the schema but not
// to this list simply stops being indexed, and queries for it fall back to a scan
// without anything failing. This test is the only thing that notices.
func TestKeywordListAttrsMatchSchema(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	schema, err := os.ReadFile("../../../../../schema/mariadb/v11/visibility/schema.sql")
	r.NoError(err)

	// Every JSON generated column in the schema is a keyword list: those are the
	// ones MySQL indexes with CAST(... AS CHAR(255) ARRAY).
	re := regexp.MustCompile(`(?m)^\s+(\w+)\s+JSON\s+GENERATED ALWAYS AS`)
	var inSchema []string
	for _, m := range re.FindAllStringSubmatch(string(schema), -1) {
		inSchema = append(inSchema, m[1])
	}
	r.NotEmpty(inSchema, "found no JSON generated columns -- has the schema moved?")

	want := append([]string(nil), inSchema...)
	got := append([]string(nil), keywordListAttrs...)
	sort.Strings(want)
	sort.Strings(got)
	r.Equal(want, got,
		"keywordListAttrs and the schema's JSON columns disagree; a column only in "+
			"the schema silently stops being indexed")
}

func TestExtractKeywordListRows(t *testing.T) {
	t.Parallel()

	row := func(sa map[string]any) *sqlplugin.VisibilityRow {
		v := sqlplugin.VisibilitySearchAttributes(sa)
		return &sqlplugin.VisibilityRow{NamespaceID: "ns", RunID: "run", SearchAttributes: &v}
	}
	longValue := make([]byte, maxKeywordListValueLen+1)
	for i := range longValue {
		longValue[i] = 'x'
	}

	t.Run("flattens lists and ignores other attributes", func(t *testing.T) {
		out, err := extractKeywordListRows(row(map[string]any{
			"BuildIds":      []string{"b2", "b1"},
			"KeywordList01": []any{"k1"},
			"Keyword01":     "not a list, not indexed",
			"Int01":         int64(7),
		}))
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{
			{"ns", "run", "BuildIds", "b1"},
			{"ns", "run", "BuildIds", "b2"},
			{"ns", "run", "KeywordList01", "k1"},
		}, out)
	})

	t.Run("drops duplicates, which would violate the primary key", func(t *testing.T) {
		out, err := extractKeywordListRows(row(map[string]any{"BuildIds": []string{"b1", "b1"}}))
		require.NoError(t, err)
		require.Len(t, out, 1)
	})

	t.Run("skips values the column cannot hold", func(t *testing.T) {
		out, err := extractKeywordListRows(row(map[string]any{
			"BuildIds": []string{"short", string(longValue)},
		}))
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{{"ns", "run", "BuildIds", "short"}}, out)
	})

	t.Run("no search attributes", func(t *testing.T) {
		out, err := extractKeywordListRows(&sqlplugin.VisibilityRow{})
		require.NoError(t, err)
		require.Empty(t, out)
	})

	t.Run("rejects a non-string element rather than guessing", func(t *testing.T) {
		_, err := extractKeywordListRows(row(map[string]any{"BuildIds": []any{1}}))
		require.Error(t, err)
	})
}
