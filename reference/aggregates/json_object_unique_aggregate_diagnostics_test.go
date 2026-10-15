package reference_test

import (
	"encoding/json"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectUniqueAggregateDiagnostics(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateSetup(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN jou_structured AS int[]", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE jou_invalid(id int,k text,v json)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO jou_invalid VALUES(1,'a','1'),(2,'a','2'),(3,'b','{"a":"\u0000"}')`, 3))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, f := range jsonObjectUniqueAggregateFamilies {
			t.Run(f.name, func(t *testing.T) {
				for _, value := range []string{"1", "NULL", "'null'::json"} {
					jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(NULL,"+value+")", jsonObjectAggregateNullKey(f.typ), false)
				}
				for _, key := range []string{"ARRAY[1]", "ROW(1)", "'{}'::json", "'{}'::jsonb", "ARRAY[1]::jou_structured"} {
					for _, value := range []string{"1", "NULL"} {
						jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"("+key+","+value+")", fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json"), false)
					}
				}
				for _, key := range []string{"", "a\"\\\n雪", "é"} {
					quoted, _ := json.Marshal(key)
					jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"($1::text,n) FROM(VALUES(1),(2)) x(n)", sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", string(quoted)), false, key)
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,1) FROM(VALUES('a'),(E'\\u0061')) x(k)", sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
				for _, tc := range []struct{ args, signature string }{{"", ""}, {"*", ""}, {"1", "integer"}, {"1,2,3", "integer, integer, integer"}} {
					q := "/*é😀*/ SELECT " + f.name + "(" + tc.args + ")"
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+f.name+"("+tc.signature+") does not exist")
					d.Hint, d.Position = hint, "15"
					jsonArrayAggregateError(t, p, b, q, d, true)
				}
				for _, tc := range []struct{ q, code, message, token, hint string }{
					{"SELECT " + f.name + "(missing) FROM jou_input", "42703", `column "missing" does not exist`, "missing", ""},
					{"SELECT " + f.name + "(k) FILTER(WHERE 2) FROM jou_input", "42804", "argument of FILTER must be type boolean, not type integer", "2)", ""},
					{"SELECT v FROM jou_input WHERE " + f.name + "(k,v) IS NULL", "42803", "aggregate functions are not allowed in WHERE", f.name, ""},
					{"SELECT v FROM jou_input GROUP BY " + f.name + "(k,v)", "42803", "aggregate functions are not allowed in GROUP BY", f.name, ""},
					{"SELECT " + f.name + "(DISTINCT k,j) FROM jou_input", "42883", "could not identify an equality operator for type json", "j)", ""},
					{"SELECT " + f.name + "(k,j ORDER BY j) FROM jou_input", "42883", "could not identify an ordering operator for type json", "j)", "Use an explicit ordering operator or modify the query."},
					{"SELECT " + f.name + "(DISTINCT k,v ORDER BY id) FROM jou_input", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "id)", ""},
					{"SELECT public." + f.name + "(k,v) FROM jou_input", "42883", "function public." + f.name + "(text, integer) does not exist", "public.", hint},
					{`SELECT "` + strings.ToUpper(f.name) + `"(k,v) FROM jou_input`, "42883", "function " + strings.ToUpper(f.name) + "(text, integer) does not exist", `"`, hint},
					{"INSERT INTO jou_input(id,v) VALUES(8," + f.name + "('a',1))", "42803", "aggregate functions are not allowed in VALUES", f.name, ""},
					{"CREATE TABLE jou_generated_" + f.name + "(v int,j json GENERATED ALWAYS AS(" + f.name + "('a',v)) STORED)", "42803", "aggregate functions are not allowed in column generation expressions", f.name + "('a'", ""},
					{"CREATE TABLE jou_default_" + f.name + "(j json DEFAULT " + f.name + "('a',1))", "42803", "aggregate functions are not allowed in DEFAULT expressions", f.name + "('a'", ""},
					{"SELECT " + f.name + "(k,jsonb_object_agg_unique(k,v)) FROM jou_input", "42803", "aggregate function calls cannot be nested", "jsonb_object_agg_unique(k,v)", ""},
					{"INSERT INTO jou_input(id,v) VALUES(8," + f.name + "(missing))", "42703", `column "missing" does not exist`, "missing", ""},
					{"INSERT INTO jou_input(id,v) VALUES(8," + f.name + "(1))", "42883", "function " + f.name + "(integer) does not exist", f.name, hint},
					{"INSERT INTO jou_input(id,v) VALUES(8," + f.name + "(1) FILTER(WHERE 2))", "42804", "argument of FILTER must be type boolean, not type integer", "2)", ""},
					{"INSERT INTO jou_input(id,v) VALUES(8,1) RETURNING " + f.name + "('a',v)", "42803", "aggregate functions are not allowed in RETURNING", f.name, ""},
					{"CREATE TABLE jou_check_" + f.name + "(v int CHECK(" + f.name + "('a',v) IS NOT NULL))", "42803", "aggregate functions are not allowed in check constraints", f.name + "('a'", ""},
					{"CREATE INDEX jou_index_" + f.name + " ON jou_input((" + f.name + "(k,v)))", "42803", "aggregate functions are not allowed in index expressions", f.name + "(k,v)", ""},
					{"SELECT v FROM jou_input LIMIT " + f.name + "(k,v)", "42803", "aggregate functions are not allowed in LIMIT", f.name, ""},
				} {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint = tc.hint
					d.Position = jsonArrayAggregatePosition(tc.q, tc.token)
					jsonArrayAggregateError(t, p, b, tc.q, d, true)
				}
				for _, prefix := range []string{"jou_generated_", "jou_default_", "jou_check_"} {
					name := prefix + f.name
					absent := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
					absent.Position = "15"
					jsonArrayAggregateError(t, p, b, "SELECT * FROM "+name, absent, true)
				}
				d := sqlJSONObjectAggregateDuplicate(false, `"a"`)
				if f.typ == "JSONB" {
					d = jsonObjectAggregateNullKey(f.typ)
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,v ORDER BY id) FROM(VALUES(1,'a',1),(2,'a',2),(3,NULL,3)) x(id,k,v)", d, false)
				d = sqlJSONObjectAggregateDuplicate(false, `"a"`)
				if f.typ == "JSONB" {
					d = fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
					d.Detail = `\u0000 cannot be converted to text.`
					d.Context = `JSON data, line 1: {"a":"\u0000...`
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,v ORDER BY id) FROM jou_invalid", d, false)
			})
		}
	})
}
