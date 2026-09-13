package mariadb

import (
	"fmt"
	"sort"

	"go.temporal.io/server/common/persistence/sql/sqlplugin"
)

// keywordListAttrs are the search attributes stored as JSON arrays -- the ones
// MySQL 8 indexes with `CAST(col AS CHAR(255) ARRAY)` multi-valued indexes and
// MariaDB cannot. Each name is the physical column it occupies in the MySQL
// schema, which is also the `attr` value used in keyword_list_search_attributes.
//
// Keep this in sync with the JSON columns in
// schema/mariadb/v11/visibility/schema.sql; TestKeywordListAttrsMatchSchema
// fails if they drift.
var keywordListAttrs = []string{
	// executions_visibility
	"TemporalChangeVersion",
	"BinaryChecksums",
	"BuildIds",
	"TemporalPauseInfo",
	"TemporalReportedProblems",
	"TemporalUsedWorkerDeploymentVersions",
	// custom_search_attributes
	"KeywordList01",
	"KeywordList02",
	"KeywordList03",
	// chasm_search_attributes
	"TemporalKeywordList01",
	"TemporalKeywordList02",
}

var keywordListAttrSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(keywordListAttrs))
	for _, a := range keywordListAttrs {
		m[a] = struct{}{}
	}
	return m
}()

// isKeywordListAttr reports whether a column is indexed through the side table.
func isKeywordListAttr(name string) bool {
	_, ok := keywordListAttrSet[name]
	return ok
}

// keywordListRow is one (execution, attribute, value) triple.
type keywordListRow struct {
	NamespaceID string `db:"namespace_id"`
	RunID       string `db:"run_id"`
	Attr        string `db:"attr"`
	Value       string `db:"value"`
}

// maxKeywordListValueLen matches VARCHAR(255) in the schema. Longer values are
// skipped rather than truncated: a truncated value would make the index claim a
// match the JSON does not have.
const maxKeywordListValueLen = 255

// extractKeywordListRows flattens a visibility row's keyword-list search
// attributes into side-table rows.
//
// Values too long for the column are dropped, and the caller keeps the JSON
// predicate alongside the index lookup so those executions are still findable.
func extractKeywordListRows(row *sqlplugin.VisibilityRow) ([]keywordListRow, error) {
	if row.SearchAttributes == nil {
		return nil, nil
	}
	sa := *row.SearchAttributes

	var out []keywordListRow
	seen := make(map[string]struct{})
	for _, attr := range keywordListAttrs {
		raw, ok := sa[attr]
		if !ok || raw == nil {
			continue
		}
		values, err := toStringSlice(raw)
		if err != nil {
			return nil, fmt.Errorf("search attribute %q: %w", attr, err)
		}
		for _, v := range values {
			if len(v) > maxKeywordListValueLen {
				continue
			}
			// The primary key is (namespace_id, run_id, attr, value), so a
			// repeated value in the array would be a duplicate-key error.
			key := attr + "\x00" + v
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, keywordListRow{
				NamespaceID: row.NamespaceID,
				RunID:       row.RunID,
				Attr:        attr,
				Value:       v,
			})
		}
	}
	// Deterministic order keeps the multi-row INSERT stable, which makes
	// deadlocks between concurrent writers to different executions less likely.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Attr != out[j].Attr {
			return out[i].Attr < out[j].Attr
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// toStringSlice accepts the shapes a KeywordList can arrive in. The search
// attribute decoder produces []string; []any turns up when a row has been
// round-tripped through JSON.
func toStringSlice(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return v, nil
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected string in list, got %T", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list of strings, got %T", raw)
	}
}
