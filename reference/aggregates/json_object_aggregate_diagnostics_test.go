package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectAggregateDiagnostics(t *testing.T) {
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectAggregateSetup(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN joa_structured_key AS integer[]", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, f := range jsonObjectAggregateFamilies {
			t.Run(f.name, func(t *testing.T) {
				for _, value := range []string{"1", "NULL", "'null'::json"} {
					jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(NULL,"+value+")", jsonObjectAggregateNullKey(f.typ), false)
				}
				for _, key := range []string{"ARRAY[1]", "ROW(1)", "'{}'::json", "'{}'::jsonb", "ARRAY[1]::joa_structured_key"} {
					values := []string{"1"}
					if !f.strict {
						values = append(values, "NULL")
					}
					for _, value := range values {
						jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"("+key+","+value+")", fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json"), false)
					}
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(1/(id-id),NULL) FROM joa_input", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false)
				for _, tc := range []struct{ args, signature string }{{"", ""}, {"*", ""}, {"1", "integer"}, {"1,2,3", "integer, integer, integer"}} {
					q := "/*é😀*/ SELECT " + f.name + "(" + tc.args + ")"
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+f.name+"("+tc.signature+") does not exist")
					d.Hint, d.Position = hint, "15"
					jsonArrayAggregateError(t, p, b, q, d, true)
				}
				for _, tc := range []struct{ prefix, reported, code, message string }{
					{"public.", "public." + f.name, "42883", ""},
					{"joa_missing.", "", "3F000", `schema "joa_missing" does not exist`},
				} {
					q := "SELECT " + tc.prefix + f.name + "(k,v) FROM joa_input"
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = "8"
					if tc.code == "42883" {
						d.Message = "function " + tc.reported + "(text, integer) does not exist"
						d.Hint = hint
					}
					jsonArrayAggregateError(t, p, b, q, d, true)
				}
				for _, name := range []string{strings.ToUpper(f.name), strings.ToUpper(f.name[:1]) + f.name[1:]} {
					q := `SELECT "` + name + `"(k,v) FROM joa_input`
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"(text, integer) does not exist")
					d.Position, d.Hint = "8", hint
					jsonArrayAggregateError(t, p, b, q, d, true)
				}
			})
		}
		for _, tc := range []struct{ q, code, message, token, hint string }{
			{"SELECT json_object_agg(missing) FROM joa_input", "42703", `column "missing" does not exist`, "missing", ""},
			{"SELECT json_object_agg(k) FILTER(WHERE 2) FROM joa_input", "42804", "argument of FILTER must be type boolean, not type integer", "2)", ""},
			{"SELECT json_object_agg(k,jsonb_object_agg(k,v)) FROM joa_input", "42803", "aggregate function calls cannot be nested", "jsonb_object_agg", ""},
			{"SELECT v FROM joa_input WHERE json_object_agg(k,v) IS NULL", "42803", "aggregate functions are not allowed in WHERE", "json_object_agg", ""},
			{"SELECT v FROM joa_input GROUP BY json_object_agg(k,v)", "42803", "aggregate functions are not allowed in GROUP BY", "json_object_agg", ""},
			{"SELECT json_object_agg(k,j ORDER BY j) FROM joa_input", "42883", "could not identify an ordering operator for type json", "j)", "Use an explicit ordering operator or modify the query."},
			{"SELECT json_object_agg(DISTINCT k,j) FROM joa_input", "42883", "could not identify an equality operator for type json", "j)", ""},
			{"SELECT jsonb_object_agg(DISTINCT k,v ORDER BY id) FROM joa_input", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "id)", ""},
			{"INSERT INTO joa_input(id,v) VALUES(8,json_object_agg(missing))", "42703", `column "missing" does not exist`, "missing", ""},
			{"INSERT INTO joa_input(id,v) VALUES(8,json_object_agg(1))", "42883", "function json_object_agg(integer) does not exist", "json_object_agg", hint},
			{"INSERT INTO joa_input(id,v) VALUES(8,json_object_agg(1) FILTER(WHERE 2))", "42804", "argument of FILTER must be type boolean, not type integer", "2)", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position, d.Hint = jsonArrayAggregatePosition(tc.q, tc.token), tc.hint
				jsonArrayAggregateError(t, p, b, tc.q, d, true)
			})
		}
		// Table input fixes conversion at execution and requires validating losing
		// duplicates, rather than relying on immutable constant-folding timing.
		for _, tc := range []struct{ payload, code, message, detail, context string }{
			{`{"a":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`},
			{`{"a":"\uDC00"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\uDC00...`},
			{`{"a":1e131072}`, "22003", "value overflows numeric format", "", ""},
		} {
			t.Run(tc.code, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE joa_invalid_"+tc.code+"(id int,k text,v json)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO joa_invalid_"+tc.code+" VALUES(1,'a',$1::json),(2,'a','1')", 2, tc.payload))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				for _, name := range []string{"jsonb_object_agg", "jsonb_object_agg_strict"} {
					jsonArrayAggregateError(t, p, b, "SELECT "+name+"(k,v ORDER BY id) FROM joa_invalid_"+tc.code, d, false)
				}
			})
		}
		// Exact case retains ordinary one-argument composite-field fallback.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TYPE joa_projection AS ("JSON_OBJECT_AGG" integer,"Jsonb_object_agg" integer)`, 0))
		for _, tc := range []struct {
			name string
			want int64
		}{{"JSON_OBJECT_AGG", 7}, {"Jsonb_object_agg", 8}} {
			q := `SELECT "` + tc.name + `"(ROW(7,8)::joa_projection) AS v`
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "INT4", tc.want))
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(`SELECT "`+tc.name+`"(NULL::joa_projection) AS v`, "INT4", nil))
		}
	})
}
