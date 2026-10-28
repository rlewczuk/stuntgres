package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceJSONBRecordComposedLayout(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_comp_pair AS (a int,b text)`, `CREATE TYPE jbr_comp_zero AS ()`)
		expectError := func(label, q, stage string, want fixture.Diagnostic, positionToken string) {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(label+"/"+prefix, func(t *testing.T) {
					query, d := prefix+q, want
					if positionToken != "" {
						index := strings.Index(query, positionToken)
						if index < 0 {
							t.Fatal("missing independent position token", query, positionToken)
						}
						d.Position = strconv.Itoa(utf8.RuneCountInString(query[:index]) + 1)
					}
					fixture.CompositeReferenceError(t, p, c, query, stage, d)
				})
			}
		}
		unknown := func(name string) fixture.Diagnostic {
			d := fixture.ControlsReferenceDiagnostic("0A000", "could not determine row type for result of "+name)
			d.Hint = jsonbRecordRowHint
			return d
		}
		inners := []struct {
			name, expression, errorFunction string
			strictNull, set, boolean        bool
		}{
			{"to object", `jsonb_to_record('{}'::jsonb)`, "jsonb_to_record", false, false, false},
			{"to SQL NULL", `jsonb_to_record(NULL::jsonb)`, "", true, false, false},
			{"populate object", `jsonb_populate_record(NULL::record,'{}'::jsonb)`, "jsonb_populate_record", false, false, false},
			{"populate SQL NULL", `jsonb_populate_record(NULL::record,NULL::jsonb)`, "jsonb_populate_record", false, false, false},
			{"to set", `jsonb_to_recordset('[{}]'::jsonb)`, "jsonb_to_recordset", false, true, false},
			{"to set SQL NULL", `jsonb_to_recordset(NULL::jsonb)`, "jsonb_to_recordset", false, true, false},
			{"populate set", `jsonb_populate_recordset(NULL::record,'[{}]'::jsonb)`, "jsonb_populate_recordset", false, true, false},
			{"populate set SQL NULL", `jsonb_populate_recordset(NULL::record,NULL::jsonb)`, "jsonb_populate_recordset", false, true, false},
			{"valid boolean", `jsonb_populate_record_valid(NULL::jbr_comp_pair,'{}'::jsonb)`, "", false, false, true},
		}
		for _, inner := range inners {
			for _, outer := range []string{"jsonb_populate_record", "jsonb_populate_recordset", "jsonb_populate_record_valid"} {
				set := outer == "jsonb_populate_recordset"
				worker := outer
				if outer == "jsonb_populate_record_valid" {
					worker = "jsonb_populate_record"
				}
				for _, nullDocument := range []bool{false, true} {
					document := `'{"a":1,"b":"x"}'::jsonb`
					if set {
						document = `'[{"a":1,"b":"x"}]'::jsonb`
					}
					if nullDocument {
						document = `NULL::jsonb`
					}
					expression := outer + "(" + inner.expression + "," + document + ")"
					label := inner.name + "/" + outer + "/" + document
					failure := unknown(inner.errorFunction)
					if inner.strictNull {
						failure = unknown(worker)
					}
					if inner.boolean {
						failure = fixture.ControlsReferenceDiagnostic("42804", "first argument of "+worker+" must be a row type")
					}
					expectError(label+"/scalar", `SELECT `+expression, "execute", failure, "")
					layout := fixture.ControlsReferenceDiagnostic("42809", "record type has not been registered")
					if outer == "jsonb_populate_record_valid" || inner.boolean {
						layout.Message = "type boolean is not composite"
					}
					expectError(label+"/expansion", `SELECT (`+expression+`).*`, "prepare", layout, "")
					from := `SELECT * FROM ` + expression + ` AS r(a int,b text)`
					if inner.set {
						expectError(label+"/typed FROM", from, "prepare", fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions must appear at top level of FROM"), inner.expression)
					} else if outer == "jsonb_populate_record_valid" || inner.boolean {
						expectError(label+"/typed FROM", from, "prepare", fixture.ControlsReferenceDiagnostic("42601", `a column definition list is only allowed for functions returning "record"`), "a int")
					} else if inner.strictNull {
						var rows [][]any
						if !nullDocument {
							rows = [][]any{{int64(1), "x"}}
						} else if !set {
							rows = [][]any{{nil, nil}}
						}
						t.Run(label+"/typed FROM", func(t *testing.T) {
							fixture.JSONEachBoth(t, p, b, c, fixture.Select(from, []string{"a", "b"}, []string{"INT4", "TEXT"}, rows))
						})
					} else {
						expectError(label+"/typed FROM", from, "execute", failure, "")
					}
				}
			}
		}
		// An outer definition cannot erase or inject a layout into a real middle call.
		for _, inner := range []string{`jsonb_to_record('{}'::jsonb)`, `jsonb_to_record(NULL::jsonb)`} {
			middle := `jsonb_populate_record(` + inner + `,'{}'::jsonb)`
			expression := `jsonb_populate_record(` + middle + `,'{"a":1}'::jsonb)`
			worker := "jsonb_to_record"
			if strings.Contains(inner, "NULL") {
				worker = "jsonb_populate_record"
			}
			expectError("deep/scalar/"+inner, `SELECT `+expression, "execute", unknown(worker), "")
			expectError("deep/expansion/"+inner, `SELECT (`+expression+`).*`, "prepare", fixture.ControlsReferenceDiagnostic("42809", "record type has not been registered"), "")
			expectError("deep/typed FROM/"+inner, `SELECT * FROM `+expression+` AS r(a int,b text)`, "execute", unknown(worker), "")
		}
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(jsonb_populate_record('(9,keep)'::jbr_comp_pair,'{"a":1}'::jsonb),'{"b":"next"}'::jsonb)::text AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"(1,next)"}}))
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_recordset(jsonb_populate_record('(9,keep)'::jbr_comp_pair,'{"a":1}'::jsonb),'[{"b":"next"},{}]'::jsonb) WITH ORDINALITY AS r(a,b,n) ORDER BY n`, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "next", int64(1)}, {int64(1), "keep", int64(2)}}))
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(jsonb_populate_record(ROW(),'{}'::jsonb),'{}'::jsonb)::text AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"()"}}))
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(ROW(9,'keep'::text),'{"f1":1}'::jsonb)::text AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"(1,keep)"}}))
		fixture.JSONEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record(ROW(9,'keep'::text),'{"f1":1}'::jsonb) AS r(a int,b text)`, []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
		expectError("ROW scalar registration is not static expansion", `SELECT (jsonb_populate_record(ROW(),'{}'::jsonb)).*`, "prepare", fixture.ControlsReferenceDiagnostic("42809", "record type has not been registered"), "")
		expectError("ROW source requires definition", `SELECT * FROM jsonb_populate_record(ROW(9,'keep'::text),'{}'::jsonb)`, "prepare", fixture.ControlsReferenceDiagnostic("42601", `a column definition list is required for functions returning "record"`), "jsonb_populate_record")
		mismatch := fixture.ControlsReferenceDiagnostic("42804", "function return row and query-specified return row do not match")
		mismatch.Detail = "Returned row contains 0 attributes, but query expects 2."
		expectError("actual ROW layout is not replaced by caller definition", `SELECT * FROM jsonb_populate_record(ROW(),'{}'::jsonb) AS r(a int,b text)`, "execute", mismatch, "")
		// A genuine named zero-member result does have a static expansion descriptor.
		q := `SELECT (jsonb_populate_record(NULL::jbr_comp_zero,'{}'::jsonb)).*`
		for _, prepared := range []bool{false, true} {
			t.Run("named zero static expansion/"+strconv.FormatBool(prepared), func(t *testing.T) {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 0 {
					t.Fatal(columns, err)
				}
				if !rows.Next() {
					t.Fatal("missing zero-member result", rows.Err())
				}
				if err := rows.Scan(); err != nil {
					t.Fatal(err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("unexpected extra zero-member row", rows.Err())
				}
			})
		}
	})
}
