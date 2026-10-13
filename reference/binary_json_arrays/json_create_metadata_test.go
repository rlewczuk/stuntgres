package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonCreateMetadata(t *testing.T, r *sql.Rows, pg bool, typ string) {
	t.Helper()
	cts, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cts) != 1 {
		t.Fatal(cts)
	}
	ct := cts[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if ct.Name() != "v" || ct.DatabaseTypeName() != typ || ct.ScanType() != scan {
		t.Fatalf("metadata %q/%q/%v", ct.Name(), ct.DatabaseTypeName(), ct.ScanType())
	}
	if n, ok := ct.Nullable(); n || ok {
		t.Fatalf("computed nullable=%v,%v", n, ok)
	}
	if n, ok := ct.Length(); ok {
		t.Fatalf("length %d", n)
	}
	if n, s, ok := ct.DecimalSize(); ok {
		t.Fatalf("decimal %d,%d", n, s)
	}
}

func TestReferenceJSONCreateMetadata(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jc_meta_pair AS (n int)`, 0))
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{`to_json(1)`, "JSON", `1`}, {`to_jsonb(1)`, "JSONB", `1`},
			{`array_to_json(ARRAY[1])`, "JSON", `[1]`}, {`row_to_json(ROW(1))`, "JSON", `{"f1":1}`},
			{`json_build_array(1)`, "JSON", `[1]`}, {`jsonb_build_array(1)`, "JSONB", `[1]`},
			{`json_build_object('a',1)`, "JSON", `{"a" : 1}`}, {`jsonb_build_object('a',1)`, "JSONB", `{"a": 1}`},
			{`to_json(NULL::int)`, "JSON", nil}, {`to_jsonb(NULL::int)`, "JSONB", nil},
			{`array_to_json(NULL::int[])`, "JSON", nil}, {`row_to_json(NULL::jc_meta_pair)`, "JSON", nil},
			{`json_build_array(VARIADIC NULL::int[])`, "JSON", nil}, {`jsonb_build_array(VARIADIC NULL::int[])`, "JSONB", nil},
			{`json_build_object(VARIADIC NULL::text[])`, "JSON", nil}, {`jsonb_build_object(VARIADIC NULL::text[])`, "JSONB", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, empty := range []bool{false, true} {
					s := jsonCreateSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.want)
					if empty {
						s.Query += " WHERE FALSE"
						s.Rows = nil
					}
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonCreateMetadata(t, r, b.PG, tc.typ) }
					s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
						var raw any
						if err := r.Scan(&raw); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, tc.typ, raw)
						if !reflect.DeepEqual(fixture.Normalize(raw), tc.want) {
							t.Fatalf("raw %#v want %#v", raw, tc.want)
						}
						var n sql.NullString
						if err := r.Scan(&n); err != nil {
							t.Fatal(err)
						}
						if n.Valid != (tc.want != nil) || (n.Valid && n.String != tc.want) {
							t.Fatal(n)
						}
						var owned []byte
						if err := r.Scan(&owned); err != nil {
							t.Fatal(err)
						}
						if tc.want == nil {
							if owned != nil {
								t.Fatal(owned)
							}
							return
						}
						if string(owned) != tc.want {
							t.Fatal(string(owned))
						}
						owned[0] = 'x'
						var text string
						if err := r.Scan(&text); err != nil || text != tc.want {
							t.Fatalf("rescan %q err=%v", text, err)
						}
					}
					jsonObjectBoth(t, p, b, c, s)
				}
			})
		}
	})
}
