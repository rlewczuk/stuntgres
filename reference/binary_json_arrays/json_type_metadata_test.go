package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT "+family+"_typeof('{}'), "+family+"_array_length('[]')", []string{family + "_typeof", family + "_array_length"}, []string{"TEXT", "INT4"}, [][]any{{"object", int64(0)}}),
			fixture.Select("SELECT "+family+"_typeof(NULL) AS kind, "+family+"_array_length(NULL) AS size", []string{"kind", "size"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}}),
			fixture.Select("SELECT * FROM "+family+"_object_keys('{}')", []string{family + "_object_keys"}, []string{"TEXT"}, [][]any{}),
			fixture.Select("SELECT "+family+"_object_keys(NULL) AS empty", []string{"empty"}, []string{"TEXT"}, [][]any{}),
			jsonTypeKeySelect(family, `{"a":1}`, [][]any{{"a", int64(1)}}),
			jsonTypeKeySelect(family, nil, [][]any{}),
		} {
			names, types := s.Columns, s.Types
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.JSONTypeMetadataCheck(t, rows, b.PG, names, types) }
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				raw := make([]any, len(types))
				dest := make([]any, len(types))
				for i := range raw {
					dest[i] = &raw[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				for i, value := range raw {
					fixture.AssertRawValue(t, b.PG, types[i], value)
				}
			}
			jsonTypeBoth(t, p, b, c, s)
		}
	}
}

func TestReferenceJSONTypeMetadata(t *testing.T) { jsonTypeScenario(t, jsonTypeMetadata) }
