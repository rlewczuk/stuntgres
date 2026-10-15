package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectUniqueAggregateValues(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateSetup(t, p, b)
		jsonObjectUniqueAggregateCore(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN jou_key AS text", 0))
		for _, f := range jsonObjectUniqueAggregateFamilies {
			t.Run(f.name, func(t *testing.T) {
				for _, values := range []string{"(1),(2)", "(NULL::int),(2)", "(1),(NULL::int)", "(NULL::int),(NULL::int)"} {
					for _, order := range []string{"v NULLS FIRST", "v DESC NULLS LAST"} {
						q := "SELECT " + f.name + "('a',v ORDER BY " + order + ") FROM(VALUES" + values + ") x(v)"
						jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
					}
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,v ORDER BY id) FROM(VALUES(1,'a',1),(2,'b',2),(3,'a',3)) x(id,k,v)", sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
				for _, tc := range []struct{ value, text, binary string }{
					{"'null'::json", `{ "a" : null }`, `{"a": null}`},
					{"'null'::jsonb", `{ "a" : null }`, `{"a": null}`},
					{`'"null"'::json`, `{ "a" : "null" }`, `{"a": "null"}`},
					{"'[null]'::json", `{ "a" : [null] }`, `{"a": [null]}`},
					{`'{"x":null}'::json`, `{ "a" : {"x":null} }`, `{"a": {"x": null}}`},
					{`'{"x":1,"x":2}'::json`, `{ "a" : {"x":1,"x":2} }`, `{"a": {"x": 2}}`},
				} {
					want := tc.text
					if f.typ == "JSONB" {
						want = tc.binary
						if f.strict && (tc.value == "'null'::json" || tc.value == "'null'::jsonb") {
							want = f.empty
						}
					}
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"('a',"+tc.value+") AS v", f.typ, want))
				}
				for _, value := range []string{"NULL::int", "'null'::json", "'null'::jsonb"} {
					jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"('a',"+value+") FROM(VALUES(1),(2)) x(n)", sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
				}
				for _, key := range []string{"true", "'true'::jou_key"} {
					want := `{ "true" : 1 }`
					if f.typ == "JSONB" {
						want = `{"true": 1}`
					}
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"("+key+",1) AS v", f.typ, want))
				}
				// Byte-distinct keys, not SQL collation or Unicode normalization equality.
				want := `{ "A" : 1, "a" : 2, "é" : 3, "é" : 4 }`
				if f.typ == "JSONB" {
					want = `{"A": 1, "a": 2, "é": 3, "é": 4}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(k,v ORDER BY v) AS v FROM(VALUES('A',1),('a',2),('é',3),('é',4)) x(k,v)", f.typ, want))
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(k,v) FILTER(WHERE v=1) AS v FROM(VALUES('a',1),('a',2),(NULL,3)) x(k,v)", f.typ, f.singleton))
				jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,"+f.name+"('a',1) AS v FROM jou_input WHERE id<>2 GROUP BY g ORDER BY g", []string{"g", "v"}, []string{"TEXT", f.typ}, [][]any{{"a", f.singleton}, {"b", f.singleton}}))
				q := "SELECT " + f.name + "(k,v) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS v FROM jou_input WHERE id<3 ORDER BY id"
				if b.PG {
					first, second := `{ "b" : 1 }`, `{ "b" : 1, "a" : 2 }`
					if f.typ == "JSONB" {
						first, second = `{"b": 1}`, `{"a": 2, "b": 1}`
					}
					jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{f.typ}, [][]any{{first}, {second}}))
				} else {
					d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
					d.Position = "8"
					jsonArrayAggregateError(t, p, b, q, d, true)
				}
				// Same unique call reuses a slot, but strict/ordinary policies stay separate.
				if f.name == "json_object_agg_unique" {
					jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT json_object_agg_unique(k,v ORDER BY id) AS u,json_object_agg_unique(k,v ORDER BY id) AS again,json_object_agg_unique_strict(k,v ORDER BY id) AS s,json_object_agg(k,v ORDER BY id) AS ordinary FROM jou_input", []string{"u", "again", "s", "ordinary"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`{ "b" : 1, "a" : 2, "c" : null }`, `{ "b" : 1, "a" : 2, "c" : null }`, `{ "b" : 1, "a" : 2 }`, `{ "b" : 1, "a" : 2, "c" : null }`}}))
				}
			})
		}
	})
}
