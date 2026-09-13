package searchattribute

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
)

// TestBoolSearchAttributeOnlyDecodesToBool pins the invariant that a Bool search
// attribute reaches a visibility store either as a Go bool or not at all -- never
// as a number, string or object.
//
// The MariaDB visibility schema depends on this. MySQL 8 extracts a Bool column
// with `search_attributes->"$.Bool01"`, which raises ERROR 3156 on a non-boolean;
// MariaDB has no `->` operator, so the column is
// `JSON_UNQUOTE(JSON_EXTRACT(...)) = 'true'`, which cannot raise -- it would
// quietly store 0 for a JSON number 1, where MySQL would have rejected the write.
// The two engines therefore only agree while this invariant holds, so if this test
// ever fails, schema/mariadb/v11/visibility/schema.sql needs revisiting rather
// than just this file.
//
// This is the check that verdict 05 (evidence/verdicts/05-schema-and-plugin-2.md,
// finding S7-3) rated LOW but explicitly could not prove.
func TestBoolSearchAttributeOnlyDecodesToBool(t *testing.T) {
	t.Parallel()

	// Bool01 is the physical column name the MariaDB generated column reads, and
	// TestNameTypeMap registers it as BOOL -- the same shape the SQL visibility
	// store gets from its search attribute provider.
	typeMap := TestNameTypeMap()
	saType, err := typeMap.GetType("Bool01")
	require.NoError(t, err)
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_BOOL, saType)

	jsonPayload := func(raw string) *commonpb.Payload {
		return &commonpb.Payload{
			Metadata: map[string][]byte{"encoding": []byte("json/plain")},
			Data:     []byte(raw),
		}
	}

	testCases := []struct {
		name     string
		raw      string
		expected any // nil means "must not reach the column at all"
	}{
		{name: "true", raw: `true`, expected: true},
		{name: "false", raw: `false`, expected: false},
		{name: "single-element list is unwrapped", raw: `[true]`, expected: true},

		// Everything below is off-contract. None of it may arrive as a non-bool.
		{name: "number 1", raw: `1`, expected: nil},
		{name: "number 0", raw: `0`, expected: nil},
		{name: "string true", raw: `"true"`, expected: nil},
		{name: "string false", raw: `"false"`, expected: nil},
		{name: "null", raw: `null`, expected: nil},
		{name: "object", raw: `{"a":1}`, expected: nil},
		{name: "string", raw: `"yes"`, expected: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)
			sa := &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{"Bool01": jsonPayload(tc.raw)},
			}

			decoded, _ := Decode(sa, &typeMap, false)
			value := decoded["Bool01"]

			if tc.expected == nil {
				// prepareSearchAttributesForDb drops nil values before writing, so a
				// nil here means the attribute never reaches the Bool column.
				r.Nil(value, "off-contract value must not reach the Bool column, got %#v", value)
				return
			}
			r.Equal(tc.expected, value)
			r.IsType(false, value, "a Bool search attribute must decode to a Go bool")
		})
	}
}
