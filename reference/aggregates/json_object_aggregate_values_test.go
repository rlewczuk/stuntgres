package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectAggregateValues(t *testing.T) {
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectAggregateSetup(t, p, b)
		jsonObjectAggregateCore(t, p, b)
		jsonObjectAggregateConvertedNulls(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN joa_key AS text", 0),
			fixture.Exec("CREATE DOMAIN joa_value AS integer", 0),
			fixture.Exec("CREATE DOMAIN joa_array_key AS integer[]", 0),
			fixture.Exec("CREATE TYPE joa_enum AS ENUM('ready','done')", 0),
			fixture.Exec("CREATE TABLE joa_pairs(id int,k text,v int)", 0),
			fixture.Exec("INSERT INTO joa_pairs VALUES(1,'a',1),(2,'a',1),(3,'a',2),(4,'b',3)", 4),
			fixture.Exec("CREATE TABLE joa_keys(id int,k text)", 0),
			fixture.Exec("INSERT INTO joa_keys VALUES(1,'é'),(2,'aa'),(3,'z'),(4,'b')", 4),
			fixture.Exec("CREATE TABLE joa_groups(id int,g int,k text,v int)", 0),
			fixture.Exec("INSERT INTO joa_groups VALUES(1,1,'a',NULL),(2,2,'b',2),(3,2,NULL,3)", 3),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, f := range jsonObjectAggregateFamilies {
			t.Run(f.name, func(t *testing.T) {
				for _, tc := range []struct{ args, j, binary string }{
					{"'a','x'", `{ "a" : "x" }`, `{"a": "x"}`},
					{"1,TRUE", `{ "1" : true }`, `{"1": true}`},
					{"TRUE,12.340::numeric", `{ "true" : 12.340 }`, `{"true": 12.340}`},
					{"DATE '2024-01-02',1", `{ "2024-01-02" : 1 }`, `{"2024-01-02": 1}`},
					{"TIMESTAMP '2024-01-02 03:04:05',1", `{ "2024-01-02T03:04:05" : 1 }`, `{"2024-01-02T03:04:05": 1}`},
					{"'ready'::joa_enum,'1'::text", `{ "ready" : "1" }`, `{"ready": "1"}`},
					{"'a'::joa_key,1::joa_value", `{ "a" : 1 }`, `{"a": 1}`},
					{"42::oid,'NaN'::numeric", `{ "42" : "NaN" }`, `{"42": "NaN"}`},
					{"'',ARRAY[1,NULL]", `{ "" : [1,null] }`, `{"": [1, null]}`},
					{"'a','[0:1]={1,NULL}'::int[]", `{ "a" : [1,null] }`, `{"a": [1, null]}`},
					{"'a',ROW(1,'x'::text)", `{ "a" : {"f1":1,"f2":"x"} }`, `{"a": {"f1": 1, "f2": "x"}}`},
					{`'a',' {"z":1,"a":1,"a":2} '::json`, `{ "a" :  {"z":1,"a":1,"a":2}  }`, `{"a": {"a": 2, "z": 1}}`},
					{`'a','{"z":1,"a":2}'::jsonb`, `{ "a" : {"a": 2, "z": 1} }`, `{"a": {"a": 2, "z": 1}}`},
				} {
					want := tc.j
					if f.typ == "JSONB" {
						want = tc.binary
					}
					jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"("+tc.args+") AS v", f.typ, want))
				}
				// Runtime-bound escaping avoids SQL string-literal settings as a second oracle.
				want := `{ "é\"\\\n" : "x\"\\\n" }`
				if f.typ == "JSONB" {
					want = `{"é\"\\\n": "x\"\\\n"}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"($1::text,$2::text) AS v", f.typ, want, "é\"\\\n", "x\"\\\n"))
				want = `{ "a" : 1, "a" : 2, "b" : 3 }`
				if f.typ == "JSONB" {
					want = `{"a": 2, "b": 3}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(DISTINCT k,v ORDER BY k,v) AS v FROM joa_pairs", f.typ, want))
				want = `{ "a" : 1, "a" : 1, "a" : 2, "b" : 3 }`
				if f.typ == "JSONB" {
					want = `{"a": 2, "b": 3}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(ALL k,v ORDER BY id) AS v FROM joa_pairs", f.typ, want))
				want = `{ "é" : 1, "aa" : 2, "z" : 3, "b" : 4 }`
				if f.typ == "JSONB" {
					want = `{"b": 4, "z": 3, "aa": 2, "é": 1}`
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT pg_catalog."+f.name+"(k,id ORDER BY id) AS v FROM joa_keys", f.typ, want))
				first := any(`{ "a" : null }`)
				second := `{ "b" : 2 }`
				if f.typ == "JSONB" {
					first = `{"a": null}`
					second = `{"b": 2}`
				}
				if f.strict {
					first = f.empty
				}
				jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,"+f.name+"(k,v ORDER BY id) FILTER(WHERE k IS NOT NULL) AS v FROM joa_groups GROUP BY g ORDER BY g", []string{"g", "v"}, []string{"INT4", f.typ}, [][]any{{int64(1), first}, {int64(2), second}}))
				if f.strict {
					for _, key := range []string{"ARRAY[1]", "ROW(1)", "'{}'::json", "'{}'::jsonb", "ARRAY[1]::joa_array_key"} {
						jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"("+key+",NULL) AS v", f.typ, f.empty))
					}
				}
			})
		}
		// Sibling providers and identical calls must neither share nor overwrite slots.
		jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT json_object_agg(k,v ORDER BY id) AS a,json_object_agg(k,v ORDER BY id) AS b,json_object_agg_strict(k,v ORDER BY id) AS s,json_agg(v ORDER BY id) AS ar FROM joa_input", []string{"a", "b", "s", "ar"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`{ "b" : 1, "a" : 2, "a" : null }`, `{ "b" : 1, "a" : 2, "a" : null }`, `{ "b" : 1, "a" : 2 }`, `[1, 2, null]`}}))
		// Real PG supports ordinary aggregate windows; native retains its provider boundary.
		q := "SELECT jsonb_object_agg(k,v) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS v FROM joa_input ORDER BY id"
		if b.PG {
			jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{`{"b": 1}`}, {`{"a": 2, "b": 1}`}, {`{"a": null, "b": 1}`}}))
		} else {
			d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
			d.Position = "8"
			jsonArrayAggregateError(t, p, b, q, d, true)
		}
	})
}

func jsonObjectAggregateConvertedNulls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE DOMAIN joa_json_null AS json", 0),
		fixture.Exec("CREATE DOMAIN joa_jsonb_null AS jsonb", 0),
		fixture.Exec("CREATE TABLE joa_strict_nulls(id int,k text,v json)", 0),
		fixture.Exec(`INSERT INTO joa_strict_nulls VALUES(1,'a','1'),(2,'bb','9'),(3,'a','null'),(4,'a','2')`, 4),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	t.Run("binary-strict-converted-null", func(t *testing.T) {
		// Converted JSON null reaches object closing, unlike SQL NULL's
		// early omission. Root's pinned PG18.0 oracle confirmed these
		// conversion and duplicate/null-compaction contracts.
		for _, value := range []string{
			`'null'::jsonb`, `' null '::json`,
			`' null '::joa_json_null`, `'null'::joa_jsonb_null`,
		} {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT jsonb_object_agg_strict('a',"+value+") AS v", "JSONB", `{}`))
			jsonArrayAggregateError(t, p, b, "SELECT jsonb_object_agg_strict(ARRAY[1],"+value+")",
				fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json"), false)
		}
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT jsonb_object_agg_strict(ARRAY[1],NULL) AS v", "JSONB", `{}`))
		for _, tc := range []struct{ value, want string }{
			{`'{"n":null}'::json`, `{"a": {"n": null}}`},
			{`'[null]'::json`, `{"a": [null]}`},
			{`'{"n":null}'::jsonb`, `{"a": {"n": null}}`},
			{`'[null]'::jsonb`, `{"a": [null]}`},
			{`'"null"'::json`, `{"a": "null"}`},
			{`'"null"'::jsonb`, `{"a": "null"}`},
		} {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT jsonb_object_agg_strict('a',"+tc.value+") AS v", "JSONB", tc.want))
		}
		for _, tc := range []struct{ order, suffix, want string }{
			{"id", " WHERE id<=3", `{"a": 1, "bb": 9}`},
			{"id DESC", " WHERE id<=3", `{"a": 1, "bb": 9}`},
			{"id", "", `{"a": 2, "bb": 9}`},
			{"id DESC", "", `{"a": 1, "bb": 9}`},
		} {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT jsonb_object_agg_strict(k,v ORDER BY "+tc.order+") AS v FROM joa_strict_nulls"+tc.suffix, "JSONB", tc.want))
		}
		// An ordinary object still admits the latest JSON null; binary
		// strict must not deduplicate first and then delete that key.
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT jsonb_object_agg(k,v ORDER BY id) AS v FROM joa_strict_nulls WHERE id<=3", "JSONB", `{"a": null, "bb": 9}`))
	})
}
