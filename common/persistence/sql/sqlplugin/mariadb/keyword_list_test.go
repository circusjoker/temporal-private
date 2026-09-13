package mariadb

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
	got := make([]string, 0, len(keywordListAttrs))
	for _, a := range keywordListAttrs {
		got = append(got, a.Name)
	}
	sort.Strings(want)
	sort.Strings(got)
	r.Equal(want, got,
		"keywordListAttrs and the schema's JSON columns disagree; a column only in "+
			"the schema silently stops being indexed")
}

func TestBuildKeywordListRows(t *testing.T) {
	t.Parallel()

	sa := func(s string) *string { return &s }
	longValue := strings.Repeat("x", maxKeywordListValueLen+1)

	t.Run("each attribute comes from its own table", func(t *testing.T) {
		// The three tables can hold different content, because their upserts are
		// guarded independently. A KeywordList must follow the table whose
		// generated column a query reads, not whichever table was checked first.
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":["b1"],"KeywordList01":["wrong-table"]}`),
			Custom:     sa(`{"KeywordList01":["k1"],"BuildIds":["wrong-table"]}`),
			Chasm:      sa(`{"TemporalKeywordList01":["c1"]}`),
		})
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{
			{"ns", "run", "BuildIds", "b1"},
			{"ns", "run", "KeywordList01", "k1"},
			{"ns", "run", "TemporalKeywordList01", "c1"},
		}, out)
	})

	t.Run("sorted, deduplicated, and non-lists ignored", func(t *testing.T) {
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":["b2","b1","b1"],"Keyword01":"not a list","Int01":7}`),
		})
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{
			{"ns", "run", "BuildIds", "b1"},
			{"ns", "run", "BuildIds", "b2"},
		}, out)
	})

	t.Run("values the column cannot hold are skipped", func(t *testing.T) {
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":["short","` + longValue + `"]}`),
		})
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{{"ns", "run", "BuildIds", "short"}}, out)
	})

	t.Run("length is counted in characters, as the column is", func(t *testing.T) {
		// 255 CJK characters fit a VARCHAR(255) but are 765 bytes.
		cjk := strings.Repeat("中", maxKeywordListValueLen)
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":["` + cjk + `"]}`),
		})
		require.NoError(t, err)
		require.Equal(t, []keywordListRow{{"ns", "run", "BuildIds", cjk}}, out)
	})

	t.Run("null attribute clears rather than errors", func(t *testing.T) {
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":null}`),
		})
		require.NoError(t, err)
		require.Empty(t, out)
	})

	t.Run("no stored rows", func(t *testing.T) {
		out, err := buildKeywordListRows("ns", "run", storedSearchAttributes{})
		require.NoError(t, err)
		require.Empty(t, out)
	})

	t.Run("rejects a non-string element rather than guessing", func(t *testing.T) {
		_, err := buildKeywordListRows("ns", "run", storedSearchAttributes{
			Executions: sa(`{"BuildIds":[1]}`),
		})
		require.Error(t, err)
	})
}
