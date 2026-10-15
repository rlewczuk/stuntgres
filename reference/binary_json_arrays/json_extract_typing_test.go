package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONExtractTyping(t *testing.T) {
	jsonExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{`SELECT '{"a":[{"b":7}]}'::json -> 'a' -> 0 ->> 'b' AS v`, "TEXT", "7", nil},
			{`SELECT '{"a":1}'::json ->> 'a' || 'x' AS v`, "TEXT", "1x", nil},
			{`SELECT '[10,20]'::json -> (0+1) AS v`, "JSON", "20", nil},
			{`SELECT json_extract_path('{"a":[10]}','a') -> 0 AS v`, "JSON", "10", nil},
			{`SELECT '{"a":1}'::json ->> json_extract_path_text('{"k":"a"}','k') AS v`, "TEXT", "1", nil},
			{`SELECT '{"a":7}'::json OPERATOR(pg_catalog.->>) 'a' AS v`, "TEXT", "7", nil},
			{`SELECT '{"a":7}'::json /*x*/ ->> /*y*/ 'a' AS v`, "TEXT", "7", nil},
			{`SELECT $1::json -> $2 AS v`, "JSON", `"zero"`, []any{`{"0":"zero"}`, "0"}},
			{`SELECT $1::json -> $2::smallint AS v`, "JSON", "10", []any{"[10]", 0}},
			{`SELECT $1::json #> $2 AS v`, "JSON", "7", []any{`{"a":7}`, "{a}"}},
			{`SELECT json_extract_path($1,$2,$3) AS v`, "JSON", "7", []any{`{"a":{"b":7}}`, "a", "b"}},
			{`SELECT json_extract_path($1,VARIADIC $2) AS v`, "JSON", "7", []any{`{"a":7}`, "{a}"}},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(tc.q, tc.typ, tc.want, tc.args...)) })
		}
	})
}

func TestReferenceJSONExtractMetadata(t *testing.T) {
	jsonExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT $1::json -> 'a', $1::json ->> 'a', $1::json #> '{a}', $1::json #>> '{a}', json_extract_path($1,'a'),json_extract_path_text($1,'a')`
		names := []string{"?column?", "?column?", "?column?", "?column?", "json_extract_path", "json_extract_path_text"}
		types := []string{"JSON", "TEXT", "JSON", "TEXT", "JSON", "TEXT"}
		for _, input := range []any{`{"a":"x"}`, `{"a":null}`, nil} {
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

func jsonExtractHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct {
		q, typ string
		cases  []struct {
			doc  string
			path any
			want any
		}
	}{
		{"SELECT $1::json #> $2 AS v", "JSON", []struct {
			doc  string
			path any
			want any
		}{{`{"x":[0],"a":[10,20,30]}`, "{a,-1}", "10"}, {`{"a":[40,50]}`, "{a,-1}", "50"}, {`{"a":[40,50]}`, "{a,0}", "40"}, {`{"a":1}`, nil, nil}}},
		{"SELECT $1::json -> $2::int AS v", "JSON", []struct {
			doc  string
			path any
			want any
		}{{"[1,2]", -1, "2"}, {"[3]", -1, "3"}, {"[1]", nil, nil}}},
		{"SELECT $1::json ->> $2 AS v", "TEXT", []struct {
			doc  string
			path any
			want any
		}{{`{"a":"x"}`, "a", "x"}, {`{"b":"y"}`, "b", "y"}, {`{"b":null}`, "b", nil}}},
		{"SELECT json_extract_path($1,VARIADIC $2) AS v", "JSON", []struct {
			doc  string
			path any
			want any
		}{{`{"x":[0],"a":[10,20,30]}`, "{a,-1}", "10"}, {`{"a":[40,50]}`, "{a,-1}", "50"}}},
		{"SELECT json_extract_path_text($1,$2) AS v", "TEXT", []struct {
			doc  string
			path any
			want any
		}{{`{"a":"x"}`, "a", "x"}, {`{"a":"z"}`, "a", "z"}}},
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
			s := jsonExtractUnicodeError(tc.q, `{"x":"\u0000"}`, `{"x":"\u0000"}`, tc.cases[0].path)
			jsonTypeRun(t, p, b, held, s)
			v := tc.cases[0]
			jsonTypeRun(t, p, b, held, jsonExtractOne(tc.q, tc.typ, v.want, v.doc, v.path))
			// The eager native reader owns imported JSON bytes, independent of the
			// caller's subsequent buffer mutation. PostgreSQL's stream has its own
			// transport ownership; both routes must retain the same selected value.
			v = tc.cases[0]
			owned := []byte(v.doc)
			rows, err := stmt.QueryContext(p.Ctx, owned, v.path)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("missing owned-input row", rows.Err())
			}
			var before any
			if err := rows.Scan(&before); err != nil {
				t.Fatal(err)
			}
			for i := range owned {
				owned[i] = 'x'
			}
			var after any
			if err := rows.Scan(&after); err != nil {
				t.Fatal(err)
			}
			cell := func(v any) any {
				if data, ok := v.([]byte); ok {
					return string(data)
				}
				return v
			}
			if cell(before) != v.want || cell(after) != v.want {
				t.Fatalf("owned row before=%v after=%v want=%v", before, after, v.want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected owned-input rows", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestReferenceJSONExtractHeld(t *testing.T) { jsonExtractScenario(t, jsonExtractHeld) }
