package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateCore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ name, typ, nonempty, allnull string }{
		{"json_agg", "JSON", "[2, null, 1]", "[null]"},
		{"jsonb_agg", "JSONB", "[2, null, 1]", "[null]"},
		{"json_agg_strict", "JSON", "[2, 1]", "[]"},
		{"jsonb_agg_strict", "JSONB", "[2, 1]", "[]"},
		{"json_arrayagg", "JSON", "[2, 1]", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct{ suffix string; want any }{
				{" WHERE g='a'", tc.nonempty}, {" WHERE g='b'", tc.allnull}, {" WHERE FALSE", nil},
			} {
				t.Run(c.suffix, func(t *testing.T) {
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+tc.name+"(v ORDER BY id) AS v FROM ja_input"+c.suffix, tc.typ, c.want))
				})
			}
			q := "SELECT "+tc.name+"(v ORDER BY id) AS v FROM ja_input WHERE FALSE GROUP BY g"
			jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{tc.typ}, nil))
			q = "SELECT "+tc.name+"(v ORDER BY id) AS v FROM ja_input GROUP BY g HAVING FALSE"
			jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{tc.typ}, nil))
			for _, order := range []string{"", " ORDER BY id"} {
				for _, accept := range []bool{false, true} {
					predicate := "FALSE"
					var want any
					if accept { predicate, want = "v IS NULL", tc.allnull }
					q = "SELECT "+tc.name+"(v"+order+") FILTER(WHERE "+predicate+") AS v FROM ja_input WHERE g='b'"
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, tc.typ, want))
				}
			}
		})
	}
	q := "SELECT json_agg(v ORDER BY id),jsonb_agg(v ORDER BY id),json_agg_strict(v ORDER BY id),jsonb_agg_strict(v ORDER BY id),json_arrayagg(v ORDER BY id) FROM ja_input WHERE g='a'"
	jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"json_agg", "jsonb_agg", "json_agg_strict", "jsonb_agg_strict", "json_arrayagg"}, []string{"JSON", "JSONB", "JSON", "JSONB", "JSON"}, [][]any{{"[2, null, 1]", "[2, null, 1]", "[2, 1]", "[2, 1]", "[2, 1]"}}))
}

func TestReferenceJSONArrayAggregateValues(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonArrayAggregateSetup(t, p, b)
		jsonArrayAggregateCore(t, p, b)
		for _, tc := range []struct{ expr, suffix, typ string; want any }{
			{"json_agg(v ORDER BY id DESC)", " WHERE g='a'", "JSON", "[1, null, 2]"},
			{"jsonb_agg_strict(v ORDER BY id DESC)", " WHERE g='a'", "JSONB", "[1, 2]"},
			{"json_agg(v ORDER BY v ASC NULLS FIRST,id DESC)", " WHERE g='a'", "JSON", "[null, 1, 2]"},
			{"json_arrayagg(v ORDER BY id NULL ON NULL)", " WHERE g='a'", "JSON", "[2, null, 1]"},
			{"json_arrayagg(v ORDER BY id ABSENT ON NULL)", " WHERE g='a'", "JSON", "[2, 1]"},
			{"json_arrayagg(v NULL ON NULL)", " WHERE g='b'", "JSON", "[null]"},
			{"json_agg_strict(j ORDER BY id)", " WHERE g='a'", "JSON", `[ {"z":1,"a":1,"a":2} , null]`},
			{"jsonb_agg_strict(j ORDER BY id)", " WHERE g='a'", "JSONB", `[{"a": 2, "z": 1}, null]`},
			{"json_agg(b ORDER BY id)", " WHERE g='a'", "JSON", `[{"a": 2, "z": 1}, null, null]`},
			{"jsonb_agg(j ORDER BY id)", " WHERE g='a'", "JSONB", `[{"a": 2, "z": 1}, null, null]`},
			{"json_agg(DISTINCT v ORDER BY v NULLS FIRST)", "", "JSON", "[null, 1, 2]"},
			{"jsonb_agg_strict(DISTINCT v ORDER BY v NULLS FIRST)", "", "JSONB", "[1, 2]"},
			{"jsonb_agg(DISTINCT b ORDER BY b NULLS FIRST)", "", "JSONB", `[null, null, {"a": 2, "z": 1}]`},
		} {
			t.Run(tc.expr+tc.suffix, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+tc.expr+" AS v FROM ja_input"+tc.suffix, tc.typ, tc.want))
			})
		}
		q := "SELECT g,json_agg(v ORDER BY id) AS a,json_arrayagg(v ORDER BY id) AS s FROM ja_input GROUP BY g"
		s := fixture.Select(q, []string{"g", "a", "s"}, []string{"TEXT", "JSON", "JSON"}, [][]any{{"a", "[2, null, 1]", "[2, 1]"}, {"b", "[null]", "[]"}})
		s.Bag = true
		jsonArrayAggregateRoutes(t, p, b, s)
		jsonArrayAggregateWindowBoundary(t,p,b)
	})
	for _, tc := range []struct{ label, setup, expr, from, json, jsonb string }{
		{"arrays", "", "v", " FROM (VALUES(1,ARRAY[1,NULL]),(2,ARRAY[2,3])) AS t(id,v)", "[[1,null], \n [2,3]]", "[[1, null], [2, 3]]"},
		{"records", "", "ROW(id,s)", " FROM (VALUES(1,'x'::text),(2,NULL::text)) AS t(id,s)", "[{\"f1\":1,\"f2\":\"x\"}, \n {\"f1\":2,\"f2\":null}]", `[{"f1": 1, "f2": "x"}, {"f1": 2, "f2": null}]`},
		{"named composite", "CREATE TYPE ja_pair AS(n int,s text)", "v", " FROM (VALUES(1,ROW(1,'x')::ja_pair),(2,ROW(NULL,NULL)::ja_pair),(3,NULL::ja_pair)) AS t(id,v)", "[{\"n\":1,\"s\":\"x\"}, \n {\"n\":null,\"s\":null}]", `[{"n": 1, "s": "x"}, {"n": null, "s": null}]`},
		{"array domain", "CREATE DOMAIN ja_array AS int[]", "v", " FROM (VALUES(1,ARRAY[1,NULL]::ja_array),(2,ARRAY[2,3]::ja_array)) AS t(id,v)", "[[1,null], \n [2,3]]", "[[1, null], [2, 3]]"},
		{"lower bounds", "", "'[0:1][2:3]={{1,2},{3,NULL}}'::int[]", " FROM (VALUES(1)) AS t(id)", "[[[1,2],[3,null]]]", "[[[1, 2], [3, null]]]"},
		{"boolean", "", "TRUE", " FROM (VALUES(1)) AS t(id)", "[true]", "[true]"},
		{"numeric", "", "12.340::numeric", " FROM (VALUES(1)) AS t(id)", "[12.340]", "[12.340]"},
		{"escaped text", "", `E'a"\\b\nc'::text`, " FROM (VALUES(1)) AS t(id)", `["a\"\\b\nc"]`, `["a\"\\b\nc"]`},
		{"NaN", "", "'NaN'::numeric", " FROM (VALUES(1)) AS t(id)", `["NaN"]`, `["NaN"]`},
		{"Infinity", "", "'Infinity'::float8", " FROM (VALUES(1)) AS t(id)", `["Infinity"]`, `["Infinity"]`},
		{"date", "", "DATE '2024-02-03'", " FROM (VALUES(1)) AS t(id)", `["2024-02-03"]`, `["2024-02-03"]`},
		{"timestamp", "", "TIMESTAMP '2024-02-03 04:05:06'", " FROM (VALUES(1)) AS t(id)", `["2024-02-03T04:05:06"]`, `["2024-02-03T04:05:06"]`},
		{"bytea", "", `'\x00ff'::bytea`, " FROM (VALUES(1)) AS t(id)", `["\\x00ff"]`, `["\\x00ff"]`},
		{"uuid", "", "'00000000-0000-0000-0000-000000000001'::uuid", " FROM (VALUES(1)) AS t(id)", `["00000000-0000-0000-0000-000000000001"]`, `["00000000-0000-0000-0000-000000000001"]`},
		{"enum", "CREATE TYPE ja_color AS ENUM('red')", "'red'::ja_color", " FROM (VALUES(1)) AS t(id)", `["red"]`, `["red"]`},
		{"range", "CREATE TABLE ja_range_input(id int,v int4range)", "v", " FROM ja_range_input", `["[1,3)"]`, `["[1,3)"]`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				if tc.setup != "" { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(tc.setup, 0)) }
				if tc.label=="range" {fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO ja_range_input VALUES(1,'[1,3)')",1))}
				for _, family := range []struct{ name, typ, want string }{{"json_agg_strict", "JSON", tc.json}, {"jsonb_agg_strict", "JSONB", tc.jsonb}} {
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+family.name+"("+tc.expr+" ORDER BY id) AS v"+tc.from, family.typ, family.want))
				}
			})
		})
	}
}

func jsonArrayAggregateWindowBoundary(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _, tc:=range []struct{name,typ,want string}{
		{"json_agg","JSON","[2, null, 1]"},{"jsonb_agg","JSONB","[2, null, 1]"},
		{"json_agg_strict","JSON","[2, 1]"},{"jsonb_agg_strict","JSONB","[2, 1]"},
		{"json_arrayagg","JSON","[2, 1]"},
	} {
		t.Run(tc.name+"/existing-window-boundary",func(t *testing.T){
			q:="SELECT "+tc.name+"(v) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS v FROM ja_input WHERE g='a' ORDER BY id"
			if b.PG {
				jsonArrayAggregateRoutes(t,p,b,fixture.Select(q,[]string{"v"},[]string{tc.typ},[][]any{{tc.want},{tc.want},{tc.want}}))
			} else {
				d:=fixture.ControlsReferenceDiagnostic("0A000","window function provider is unsupported")
				d.Position="8"
				jsonArrayAggregateError(t,p,b,q,d,true)
			}
		})
	}
}
