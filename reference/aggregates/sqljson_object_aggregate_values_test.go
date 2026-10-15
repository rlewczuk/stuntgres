package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLJSONObjectAggregateValues(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE sjo_filter_input(n int)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO sjo_filter_input VALUES(1)", 1))
		for _, tc := range []struct{ expr, typ, want string }{
			{`json_objectagg('a' VALUE 1)`, "JSON", `{ "a" : 1 }`},
			{`json_objectagg('a':1)`, "JSON", `{ "a" : 1 }`},
			{`json_objectagg('a':NULL)`, "JSON", `{ "a" : null }`},
			{`json_objectagg('a':NULL NULL ON NULL)`, "JSON", `{ "a" : null }`},
			{`json_objectagg('a':NULL ABSENT ON NULL)`, "JSON", `{  }`},
			{`json_objectagg('a':NULL RETURNING jsonb)`, "JSONB", `{"a": null}`},
			{`json_objectagg('a':NULL ABSENT ON NULL RETURNING jsonb)`, "JSONB", `{}`},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+tc.expr+" AS v", tc.typ, tc.want))
			})
		}
		for _, returning := range []string{"", " RETURNING jsonb"} {
			typ := "JSON"
			if returning != "" {
				typ = "JSONB"
			}
			for _, suffix := range []string{" WHERE FALSE", " WHERE FALSE GROUP BY g"} {
				t.Run(returning+suffix, func(t *testing.T) {
					s := jsonArrayAggregateSelect("SELECT json_objectagg(k:v"+returning+") AS v FROM (VALUES ('a',1,1)) t(k,v,g)"+suffix, typ, nil)
					if suffix == " WHERE FALSE GROUP BY g" {
						s.Rows = nil
					}
					jsonArrayAggregateRoutes(t, p, b, s)
				})
			}
			for _, filter := range []string{"FALSE", "NULL::boolean"} {
				t.Run(returning+filter, func(t *testing.T) {
					// A relation column prevents VALUES substitution and planner
					// constant folding from evaluating the division before FILTER.
					q := "SELECT json_objectagg(NULL:1/(n-n) WITH UNIQUE" + returning + ") FILTER(WHERE " + filter + ") AS v FROM sjo_filter_input"
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, typ, nil))
				})
			}
		}
		for _, separator := range []string{":", " VALUE "} {
			t.Run(separator+"/ordered-input", func(t *testing.T) {
				q := "SELECT json_objectagg(k" + separator + "v) AS v FROM (SELECT * FROM (VALUES (2,'b',NULL::int),(1,'a',1)) t(id,k,v) ORDER BY id) s"
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSON", `{ "a" : 1, "b" : null }`))
			})
		}
		q := "SELECT g,json_objectagg(k:v WITH UNIQUE KEYS) AS v FROM (VALUES (1,'a',1),(2,'a',2)) t(g,k,v) GROUP BY g ORDER BY g"
		jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"g", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `{ "a" : 1 }`}, {int64(2), `{ "a" : 2 }`}}))
		q = "SELECT json_objectagg(k:v WITH UNIQUE) FILTER(WHERE id=1) AS v,json_objectagg(k:v WITH UNIQUE) FILTER(WHERE id=2) AS sibling FROM (VALUES (1,'a',1),(2,'a',2)) t(id,k,v)"
		jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v", "sibling"}, []string{"JSON", "JSON"}, [][]any{{`{ "a" : 1 }`, `{ "a" : 2 }`}}))
	})
}

func TestReferenceSQLJSONObjectAggregateNullUnique(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, family := range []struct {
			clause, typ string
			binary      bool
		}{
			{"", "JSON", false}, {" RETURNING jsonb", "JSONB", true}, {" RETURNING jsonb FORMAT JSON", "JSONB", false},
		} {
			for _, policy := range []string{" NULL ON NULL", " ABSENT ON NULL"} {
				for _, values := range []string{"(1,1),(2,2)", "(1,NULL::int),(2,2)", "(1,1),(2,NULL::int)", "(1,NULL::int),(2,NULL::int)"} {
					for _, unique := range []string{" WITH UNIQUE", " WITH UNIQUE KEYS"} {
						t.Run(family.clause+policy+values+unique, func(t *testing.T) {
							q := "SELECT json_objectagg('a':v" + policy + unique + family.clause + ") AS v FROM (SELECT * FROM (VALUES " + values + ") t(id,v) ORDER BY id) s"
							jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(family.binary, `"a"`), false)
						})
					}
				}
				for _, unique := range []string{"", " WITH UNIQUE", " WITHOUT UNIQUE KEYS"} {
					t.Run("null-key/"+family.clause+policy+unique, func(t *testing.T) {
						q := "SELECT json_objectagg(NULL:NULL" + policy + unique + family.clause + ") AS v"
						typ := "JSON"
						if family.binary {
							typ = "JSONB"
						}
						jsonArrayAggregateError(t, p, b, q, jsonObjectAggregateNullKey(typ), false)
					})
				}
			}
			for _, unique := range []string{"", " WITHOUT UNIQUE", " WITHOUT UNIQUE KEYS"} {
				t.Run("duplicates-allowed/"+family.clause+unique, func(t *testing.T) {
					q := "SELECT json_objectagg('a':v" + unique + family.clause + ") AS v FROM (SELECT * FROM (VALUES (1,1),(2,2)) t(id,v) ORDER BY id) s"
					want := `{ "a" : 1, "a" : 2 }`
					if family.typ == "JSONB" {
						want = `{"a": 2}`
					}
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, family.typ, want))
				})
			}
		}
		for _, returning := range []string{"", " RETURNING jsonb"} {
			t.Run("nested/"+returning, func(t *testing.T) {
				typ, want := "JSON", `{ "a" :  {"x":1,"x":2}  }`
				if returning != "" {
					typ, want = "JSONB", `{"a": {"x": 2}}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':' {\"x\":1,\"x\":2} '::json WITH UNIQUE"+returning+") AS v", typ, want))
			})
		}
		for _, binary := range []bool{false, true} {
			t.Run(map[bool]string{false: "escaped-json", true: "escaped-jsonb"}[binary], func(t *testing.T) {
				clause := ""
				if binary {
					clause = " RETURNING jsonb"
				}
				q := "SELECT json_objectagg($1::text:v WITH UNIQUE" + clause + ") AS v FROM (VALUES (1),(2)) t(v)"
				jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(binary, `"a\"\\\n"`), false, "a\"\\\n")
			})
		}
	})
}

func TestReferenceSQLJSONObjectAggregateTiming(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN sjo_timing_jsonb AS jsonb", 0))
		for _, family := range []struct {
			clause string
			binary bool
		}{
			{"", false}, {" RETURNING jsonb", true}, {" RETURNING jsonb FORMAT JSON", false}, {" RETURNING sjo_timing_jsonb", false},
		} {
			for _, later := range []string{"null-key", "bad-format"} {
				t.Run(family.clause+later, func(t *testing.T) {
					pair, source := "k:v", "(VALUES (1,'a',1),(2,'a',2),(3,NULL,3))"
					d := sqlJSONObjectAggregateDuplicate(false, `"a"`)
					if later == "bad-format" {
						pair, source = "k:v FORMAT JSON", "(VALUES (1,'a','1'),(2,'a','2'),(3,'b','no'))"
						if family.binary {
							d = fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
							d.Detail, d.Context = `Token "no" is invalid.`, "JSON data, line 1: no"
						}
					} else if family.binary {
						d = jsonObjectAggregateNullKey("JSONB")
					}
					q := "SELECT json_objectagg(" + pair + " WITH UNIQUE" + family.clause + ") AS v FROM (SELECT * FROM " + source + " t(id,k,v) ORDER BY id) s"
					jsonArrayAggregateError(t, p, b, q, d, false)
				})
			}
		}
		for _, tc := range []struct{ clause, typ, root string }{
			{"", "JSON", `{ "a" : null }`},
			{" RETURNING jsonb", "JSONB", `{}`},
			{" RETURNING jsonb FORMAT JSON", "JSONB", `{"a": null}`},
			{" RETURNING sjo_timing_jsonb", "JSONB", `{"a": null}`},
		} {
			for _, input := range []string{"'null'::json", "'null'::jsonb", "'[null]'::json", "NULL::json"} {
				t.Run(tc.clause+input, func(t *testing.T) {
					typ, want := tc.typ, tc.root
					if input == "NULL::json" {
						want = `{  }`
						if typ == "JSONB" {
							want = `{}`
						}
					}
					if input == "'[null]'::json" {
						want = `{ "a" : [null] }`
						if typ == "JSONB" {
							want = `{"a": [null]}`
						}
					}
					if input == "'null'::jsonb" && tc.clause == "" {
						typ, want = "JSONB", `{}`
					}
					q := "SELECT json_objectagg('a':" + input + " ABSENT ON NULL" + tc.clause + ") AS v"
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, typ, want))
				})
			}
		}
	})
}
