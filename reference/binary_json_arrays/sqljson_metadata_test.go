package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONMetadata(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ, name string
			want            any
		}{
			{`JSON_ARRAY(1)`, "JSON", "json_array", `[1]`},
			{`JSON_OBJECT('a':1)`, "JSON", "json_object", `{"a" : 1}`},
			{`JSON('{}')`, "JSON", "json", `{}`},
			{`JSON_SCALAR(1)`, "JSON", "json_scalar", `1`},
			{`JSON_SERIALIZE('{}')`, "TEXT", "json_serialize", `{}`},
			{`'{}' IS JSON`, "BOOL", "?column?", true},
			{`JSON_ARRAY('{}'::jsonb)`, "JSONB", "json_array", `[{}]`},
			{`JSON_ARRAY(SELECT 1 WHERE FALSE)`, "JSON", "json_array", nil},
			{`JSON_ARRAY(1 RETURNING bytea)`, "BYTEA", "json_array", `[1]`},
			{`JSON(NULL::text)`, "JSON", "json", nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, empty := range []bool{false, true} {
					name := "populated"
					if empty {
						name = "empty"
					}
					t.Run(name, func(t *testing.T) {
						s := fixture.Select("SELECT "+tc.expr, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
						if empty {
							s.Query += " WHERE FALSE"
							s.Rows = nil
						}
						s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
							cts, err := r.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							if len(cts) != 1 {
								t.Fatal(cts)
							}
							ct := cts[0]
							wantScan := reflect.TypeFor[string]()
							if tc.typ == "BOOL" {
								wantScan = reflect.TypeFor[bool]()
							}
							if tc.typ == "BYTEA" {
								wantScan = reflect.TypeFor[[]byte]()
							}
							if b.PG && (tc.typ == "JSON" || tc.typ == "JSONB") {
								wantScan = reflect.TypeFor[any]()
							}
							if ct.ScanType() != wantScan {
								t.Fatalf("ScanType %v want %v", ct.ScanType(), wantScan)
							}
							if n, ok := ct.Nullable(); n || ok {
								t.Fatalf("Nullable %v,%v", n, ok)
							}
							wantLength, wantLengthOK := int64(0), false
							if tc.typ == "TEXT" || b.PG && tc.typ == "BYTEA" {
								wantLength, wantLengthOK = math.MaxInt64, true
							}
							if n, ok := ct.Length(); n != wantLength || ok != wantLengthOK {
								t.Fatalf("Length %d,%v want %d,%v", n, ok, wantLength, wantLengthOK)
							}
							if n, scale, ok := ct.DecimalSize(); ok {
								t.Fatalf("DecimalSize %d,%d,%v", n, scale, ok)
							}
						}
						s.CheckScan = func(t *testing.T, r *sql.Rows, _ int) {
							var raw any
							if err := r.Scan(&raw); err != nil {
								t.Fatal(err)
							}
							fixture.AssertRawValue(t, b.PG, tc.typ, raw)
							if !reflect.DeepEqual(fixture.Normalize(raw), tc.want) {
								t.Fatalf("raw %#v want %#v", raw, tc.want)
							}
						}
						sqljsonBoth(t, p, b, c, s)
					})
				}
			})
		}
	})
}

func TestReferenceSQLJSONLegacyResolution(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqljsonValues(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`JSON_OBJECT(ARRAY['a','1','a',NULL])`, "JSON", `{"a" : "1", "a" : null}`},
			{`JSON_OBJECT(ARRAY[['a','1'],['b','2']])`, "JSON", `{"a" : "1", "b" : "2"}`},
			{`JSON_OBJECT('[0:1]={a,b}'::text[],'[3:4]={1,2}'::text[])`, "JSON", `{"a" : "1", "b" : "2"}`},
			{`JSON_OBJECT(ARRAY[]::text[])`, "JSON", `{}`},
			{`JSON_OBJECT(NULL::text[])`, "JSON", nil},
			{`pg_catalog.json_object(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`"json_object"(ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`pg_catalog.json_object(VARIADIC ARRAY['a','1'])`, "JSON", `{"a" : "1"}`},
			{`JSONB_OBJECT(ARRAY['a','1','a',NULL])`, "JSONB", `{"a": null}`},
			{`JSONB_OBJECT(ARRAY[]::text[])`, "JSONB", `{}`},
		})
		for _, tc := range []struct{ expr, code, message string }{
			{`JSON_OBJECT(ARRAY['a'])`, "2202E", "array must have even number of elements"},
			{`JSON_OBJECT(ARRAY[['a','1','x']])`, "2202E", "array must have two columns"},
			{`JSON_OBJECT(ARRAY['a'],ARRAY['1','2'])`, "2202E", "mismatched array dimensions"},
			{`JSON_OBJECT(ARRAY[NULL,'1'])`, "22004", "null value not allowed for object key"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				sqljsonDiagnostic(t, p, b, c, "SELECT "+tc.expr+" AS v", fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
	})
}
