package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBExtractTyping(t *testing.T) {
	jsonbExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{`SELECT '{"a":[{"b":7}]}'::jsonb -> 'a' -> 0 ->> 'b' AS v`, "TEXT", "7", nil},
			{`SELECT '{"a":1}'::jsonb ->> 'a' || 'x' AS v`, "TEXT", "1x", nil},
			{`SELECT '[10,20]'::jsonb -> (0+1) AS v`, "JSONB", "20", nil},
			{`SELECT jsonb_extract_path('{"a":[10]}','a') -> 0 AS v`, "JSONB", "10", nil},
			{`SELECT '{"a":7}'::jsonb OPERATOR(pg_catalog.->>) 'a' AS v`, "TEXT", "7", nil},
			{`SELECT pg_catalog.jsonb_extract_path_text('{"a":7}','a') AS v`, "TEXT", "7", nil},
			{`SELECT jsonb_extract_path(from_json=>'{"a":7}',VARIADIC path_elems=>'{a}'::text[]) AS v`, "JSONB", "7", nil},
			{`SELECT jsonb_extract_path_text(from_json=>'{"a":7}',VARIADIC path_elems=>'{a}'::text[]) AS v`, "TEXT", "7", nil},
			{`SELECT $1::jsonb -> $2 AS v`, "JSONB", `"zero"`, []any{`{"0":"zero"}`, "0"}},
			{`SELECT $1::jsonb -> $2::smallint AS v`, "JSONB", "10", []any{"[10]", 0}},
			{`SELECT $1::jsonb #> $2 AS v`, "JSONB", "7", []any{`{"a":7}`, "{a}"}},
			{`SELECT jsonb_extract_path($1,$2,$3) AS v`, "JSONB", "7", []any{`{"a":{"b":7}}`, "a", "b"}},
			{`SELECT jsonb_extract_path_text($1,$2,$3) AS v`, "TEXT", "7", []any{`{"a":{"b":7}}`, "a", "b"}},
			{`SELECT jsonb_extract_path($1,VARIADIC $2) AS v`, "JSONB", "7", []any{`{"a":7}`, "{a}"}},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(tc.q, tc.typ, tc.want, tc.args...)) })
		}
		jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE DOMAIN jbex_doc AS jsonb`, 0))
		jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"a":7}'::jbex_doc -> 'a' AS v`, "JSONB", "7"))
		jsonTypeRun(t, p, b, c, fixture.Exec(`SET search_path TO public`, 0))
		jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT jsonb_extract_path('{"a":7}','a') AS v`, "JSONB", "7"))
		jsonTypeRun(t, p, b, c, fixture.Exec(`SET search_path TO public,pg_catalog`, 0))
		jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT pg_catalog.jsonb_extract_path_text('{"a":7}','a') AS v`, "TEXT", "7"))
	})
}

func TestReferenceJSONBExtractMetadata(t *testing.T) {
	jsonbExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT $1::jsonb -> 'a', $1::jsonb ->> 'a', $1::jsonb #> '{a}', $1::jsonb #>> '{a}', jsonb_extract_path($1,'a'),jsonb_extract_path_text($1,'a')`
		names := []string{"?column?", "?column?", "?column?", "?column?", "jsonb_extract_path", "jsonb_extract_path_text"}
		types := []string{"JSONB", "TEXT", "JSONB", "TEXT", "JSONB", "TEXT"}
		for _, input := range []any{`{"a":"x"}`, `{"a":null}`, `{}`, nil} {
			var raw, text any
			if input == `{"a":"x"}` {
				raw, text = `"x"`, "x"
			}
			if input == `{"a":null}` {
				raw = "null"
			}
			jsonExtractBoth(t, p, b, c, fixture.Select(q, names, types, [][]any{{raw, text, raw, text, raw, text}}, input))
			jsonExtractBoth(t, p, b, c, fixture.Select(q+" WHERE false", names, types, nil, input))
		}
	})
}

func jsonbExtractHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	type execution struct {
		doc        any
		path, want any
	}
	for _, tc := range []struct {
		q, typ string
		cases  []execution
	}{
		{"SELECT $1::jsonb -> $2::int AS v", "JSONB", []execution{{"[1,2]", -1, "2"}, {"[3]", -1, "3"}, {"[3]", 1, nil}, {"[3]", nil, nil}, {nil, 0, nil}}},
		{"SELECT $1::jsonb ->> $2 AS v", "TEXT", []execution{{`{"a":"x"}`, "a", "x"}, {`{"b":"y"}`, "b", "y"}, {`{"b":null}`, "b", nil}, {`{}`, "b", nil}}},
		{"SELECT $1::jsonb #> $2 AS v", "JSONB", []execution{{`{"x":[0],"a":[10,20,30]}`, "{a,-1}", "30"}, {`{"a":[40,50]}`, "[0:1]={a,-1}", "50"}, {`{"a":7}`, "{}", `{"a": 7}`}, {`{}`, "{a,NULL}", nil}}},
		{"SELECT jsonb_extract_path($1,VARIADIC $2) AS v", "JSONB", []execution{{`{"a":[10,20,30]}`, "{a,-1}", "30"}, {`{"a":[40]}`, "{a,-1}", "40"}, {`{"a":7}`, nil, nil}}},
		{"SELECT jsonb_extract_path_text($1,$2) AS v", "TEXT", []execution{{`{"a":"x"}`, "a", "x"}, {`{"a":"z"}`, "a", "z"}}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
			for range 2 {
				for _, v := range tc.cases {
					jsonTypeRun(t, p, b, held, jsonEachWithMetadata(jsonExtractOne(tc.q, tc.typ, v.want, v.doc, v.path), b.PG))
				}
			}
			jsonTypeRun(t, p, b, held, jsonbExtractInputError(tc.q, true, "oops", tc.cases[0].path))
			v := tc.cases[0]
			jsonTypeRun(t, p, b, held, jsonExtractOne(tc.q, tc.typ, v.want, v.doc, v.path))
			owned := []byte(v.doc.(string))
			rows, err := stmt.QueryContext(p.Ctx, owned, v.path)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !b.PG {
				// Native eager cursors remain detached while this SAME handle runs again.
				next := tc.cases[1]
				jsonTypeRun(t, p, b, held, jsonExtractOne(tc.q, tc.typ, next.want, next.doc, next.path))
			}
			if !rows.Next() {
				t.Fatal("missing owned row", rows.Err())
			}
			var before, after any
			if err := rows.Scan(&before); err != nil {
				t.Fatal(err)
			}
			for i := range owned {
				owned[i] = 'x'
			}
			if err := rows.Scan(&after); err != nil {
				t.Fatal(err)
			}
			if fixture.Normalize(before) != v.want || fixture.Normalize(after) != v.want {
				t.Fatalf("retained cell %v/%v want %v", before, after, v.want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected owned rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			jsonTypeRun(t, p, b, held, jsonExtractOne(tc.q, tc.typ, v.want, v.doc, v.path))
		})
	}
}

func TestReferenceJSONBExtractHeld(t *testing.T) { jsonbExtractScenario(t, jsonbExtractHeld) }
