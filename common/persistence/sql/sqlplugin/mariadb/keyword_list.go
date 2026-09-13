package mariadb

import (
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

// sourceTable is the table whose generated column a keyword list is read from.
//
// It matters which one: the three visibility tables are written from the same
// JSON in the same transaction, but their upserts are guarded independently, so
// they can hold different content. A query for KeywordList01 reads
// custom_search_attributes' generated column, so the index for KeywordList01
// must be built from custom_search_attributes and nothing else.
type sourceTable int

const (
	sourceExecutions sourceTable = iota
	sourceCustom
	sourceChasm
)

// keywordListAttr is a search attribute stored as a JSON array -- the ones MySQL 8
// indexes with `CAST(col AS CHAR(255) ARRAY)` multi-valued indexes and MariaDB
// cannot. Name is the physical column, which is also the `attr` value in
// keyword_list_search_attributes.
//
// Keep in sync with the JSON columns in schema/mariadb/v11/visibility/schema.sql;
// TestKeywordListAttrsMatchSchema fails if they drift.
type keywordListAttr struct {
	Name   string
	Source sourceTable
}

var keywordListAttrs = []keywordListAttr{
	{"TemporalChangeVersion", sourceExecutions},
	{"BinaryChecksums", sourceExecutions},
	{"BuildIds", sourceExecutions},
	{"TemporalPauseInfo", sourceExecutions},
	{"TemporalReportedProblems", sourceExecutions},
	{"TemporalUsedWorkerDeploymentVersions", sourceExecutions},
	{"KeywordList01", sourceCustom},
	{"KeywordList02", sourceCustom},
	{"KeywordList03", sourceCustom},
	{"TemporalKeywordList01", sourceChasm},
	{"TemporalKeywordList02", sourceChasm},
}

var keywordListAttrSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(keywordListAttrs))
	for _, a := range keywordListAttrs {
		m[a.Name] = struct{}{}
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
	NamespaceID string
	RunID       string
	Attr        string
	Value       string
}

// maxKeywordListValueLen matches VARCHAR(255) in the schema, counted in
// characters as the column is. Longer values are skipped rather than truncated: a
// truncated value would make the index claim a match the JSON does not have. The
// query converter applies the same rule, so nothing asks the index about a value
// it cannot hold.
const maxKeywordListValueLen = 255

func indexable(v string) bool { return utf8.RuneCountInString(v) <= maxKeywordListValueLen }

// storedSearchAttributes is the JSON actually committed to each of the three
// visibility tables, read back inside the write's own transaction.
type storedSearchAttributes struct {
	Executions *string `db:"ev_sa"`
	Custom     *string `db:"csa_sa"`
	Chasm      *string `db:"chasm_sa"`
}

func (s storedSearchAttributes) forSource(src sourceTable) *string {
	switch src {
	case sourceExecutions:
		return s.Executions
	case sourceCustom:
		return s.Custom
	case sourceChasm:
		return s.Chasm
	}
	return nil
}

// buildKeywordListRows flattens the *stored* search attributes into side-table
// rows.
//
// Deriving from what the database holds rather than from the row we tried to
// write is what makes the index agree with the column by construction. The
// alternative -- deriving from the in-memory row and guarding with a version
// check -- has to replicate the upsert's exact semantics, and a guard that says
// `!=` where the SQL says `<` silently rewrites the index for a write the row
// rejected.
func buildKeywordListRows(
	namespaceID string,
	runID string,
	stored storedSearchAttributes,
) ([]keywordListRow, error) {
	decoded := map[sourceTable]map[string]json.RawMessage{}
	for _, src := range []sourceTable{sourceExecutions, sourceCustom, sourceChasm} {
		raw := stored.forSource(src)
		if raw == nil || *raw == "" {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*raw), &m); err != nil {
			return nil, fmt.Errorf("stored search attributes are not a JSON object: %w", err)
		}
		decoded[src] = m
	}

	var out []keywordListRow
	seen := make(map[string]struct{})
	for _, attr := range keywordListAttrs {
		m, ok := decoded[attr.Source]
		if !ok {
			continue
		}
		raw, ok := m[attr.Name]
		if !ok {
			continue
		}
		values, err := decodeStringList(raw)
		if err != nil {
			return nil, fmt.Errorf("search attribute %q: %w", attr.Name, err)
		}
		for _, v := range values {
			if !indexable(v) {
				continue
			}
			// The primary key is (namespace_id, run_id, attr, value), so a
			// repeated value in the array would be a duplicate-key error.
			key := attr.Name + "\x00" + v
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, keywordListRow{namespaceID, runID, attr.Name, v})
		}
	}
	// Deterministic order keeps the multi-row INSERT stable, which makes
	// deadlocks between concurrent writers less likely.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Attr != out[j].Attr {
			return out[i].Attr < out[j].Attr
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// decodeStringList accepts the shapes a KeywordList is stored in: a JSON array of
// strings, or a bare string. A JSON null means the attribute was cleared.
func decodeStringList(raw json.RawMessage) ([]string, error) {
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var isNull any
	if err := json.Unmarshal(raw, &isNull); err == nil && isNull == nil {
		return nil, nil
	}
	return nil, fmt.Errorf("expected a list of strings, got %s", string(raw))
}
