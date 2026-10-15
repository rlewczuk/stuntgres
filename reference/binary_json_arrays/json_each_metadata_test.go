package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonEachMetadata(t *testing.T, r *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	meta, err := r.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatalf("metadata=%v error=%v", meta, err)
	}
	for i, ct := range meta {
		scan := reflect.TypeFor[string]()
		if types[i] == "INT8" {
			scan = reflect.TypeFor[int64]()
		}
		if types[i] == "INT4" {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		// pq reports interface{} for JSON and RECORD; their raw carriers
		// remain []byte. Confirmed against the pinned PostgreSQL 18.0 oracle.
		if pg && (types[i] == "RECORD" || types[i] == "JSON") {
			scan = reflect.TypeFor[any]()
		}
		if ct.Name() != names[i] || ct.DatabaseTypeName() != types[i] || ct.ScanType() != scan {
			t.Errorf("column%d=%q/%q/%v want=%q/%q/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), names[i], types[i], scan)
		}
		length, known := int64(0), false
		if types[i] == "TEXT" {
			length, known = 9223372036854775807, true
		}
		if n, k := ct.Length(); n != length || k != known {
			t.Errorf("column%d Length=%d/%t want=%d/%t", i, n, k, length, known)
		}
		if n, k := ct.Nullable(); n || k {
			t.Errorf("computed column%d Nullable=%t/%t", i, n, k)
		}
		if p, s, k := ct.DecimalSize(); p != 0 || s != 0 || k {
			t.Errorf("column%d DecimalSize=%d/%d/%t", i, p, s, k)
		}
	}
}

func jsonEachWithMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonEachMetadata(t, rows, pg, s.Columns, s.Types) }
	return s
}

func TestReferenceJSONEachMetadata(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"json_each", "json_each_text"} {
			for _, input := range []any{`{"a":1}`, `{}`, nil} {
				for _, tc := range []struct {
					q            string
					names, types []string
					row          []any
				}{
					{"SELECT " + f + "($1)", []string{f}, []string{"RECORD"}, []any{"(a,1)"}},
					{"SELECT " + f + "($1) AS r", []string{"r"}, []string{"RECORD"}, []any{"(a,1)"}},
					{"SELECT * FROM " + f + "($1)", []string{"key", "value"}, []string{"TEXT", jsonEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j", []string{"key", "value"}, []string{"TEXT", jsonEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j(k,v)", []string{"k", "v"}, []string{"TEXT", jsonEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j(k)", []string{"k", "value"}, []string{"TEXT", jsonEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) WITH ORDINALITY AS j(k,v,n) ORDER BY n", []string{"k", "v", "n"}, []string{"TEXT", jsonEachType(f), "INT8"}, []any{"a", "1", int64(1)}},
				} {
					t.Run(f+tc.q, func(t *testing.T) {
						var want [][]any
						if input == `{"a":1}` {
							want = [][]any{tc.row}
						}
						jsonEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(tc.q, tc.names, tc.types, want, input), b.PG))
					})
				}
			}
			value := "(a,null)"
			if f == "json_each_text" {
				value = "(a,)"
			}
			jsonEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select("SELECT "+f+"('{\"a\":null}'::json)", []string{f}, []string{"RECORD"}, [][]any{{value}}), b.PG))
		}
	})
}
