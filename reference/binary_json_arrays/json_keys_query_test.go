package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonKeysSources(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jk_source(id int,j json)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO jk_source VALUES(1,$1),(2,$2),(3,$3),(4,$4)", 4, `{"a":1,"b":2}`, `{}`, nil, `{"a":1,"a":2}`)))
}

func TestReferenceJSONKeysQueryStructure(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonKeysSources(t, p, b, c)
		for _, tc := range []struct {
			q              string
			columns, types []string
			rows           [][]any
			ordered        bool
		}{
			{`SELECT 7,json_object_keys('{}'::json)`, []string{"?column?", "json_object_keys"}, []string{"INT4", "TEXT"}, nil, false},
			{`SELECT json_object_keys('{"a":1,"b":2}'::json),string_to_table('x',',')`, []string{"json_object_keys", "string_to_table"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", nil}}, false},
			{`SELECT json_object_keys('{"a":1,"b":2}'::json),string_to_table('x,y,z',',')`, []string{"json_object_keys", "string_to_table"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "y"}, {nil, "z"}}, false},
			{`SELECT json_object_keys('{}'),string_to_table('',',')`, []string{"json_object_keys", "string_to_table"}, []string{"TEXT", "TEXT"}, nil, false},
			{`SELECT l.k,r.k FROM json_object_keys('{"a":1,"b":2}') AS l(k),json_object_keys('{"x":1,"y":2}') AS r(k)`, []string{"k", "k"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}, false},
			{`SELECT * FROM ROWS FROM (json_object_keys('{"a":1,"b":2}'),string_to_table('x',',')) WITH ORDINALITY AS r(k,s,n) ORDER BY n`, []string{"k", "s", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", "x", int64(1)}, {"b", nil, int64(2)}}, true},
			{`SELECT k,v[1] AS v FROM json_object_keys('{"a":1,"b":2}') AS j(k),regexp_matches('xy','(.)','g') AS r(v)`, []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}, false},
			{`SELECT id,json_object_keys(j) FROM jk_source`, []string{"id", "json_object_keys"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(4), "a"}, {int64(4), "a"}}, false},
			{`SELECT id,k FROM jk_source,json_object_keys(j) AS f(k)`, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(4), "a"}, {int64(4), "a"}}, false},
			{`SELECT id,k FROM jk_source CROSS JOIN LATERAL json_object_keys(j) AS f(k)`, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(4), "a"}, {int64(4), "a"}}, false},
			{`SELECT id,k FROM jk_source LEFT JOIN LATERAL json_object_keys(j) AS f(k) ON true`, []string{"id", "k"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(2), nil}, {int64(3), nil}, {int64(4), "a"}, {int64(4), "a"}}, false},
			{`SELECT id,k,n FROM jk_source,json_object_keys(j) WITH ORDINALITY AS f(k,n) ORDER BY id,n`, []string{"id", "k", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(1), "b", int64(2)}, {int64(4), "a", int64(1)}, {int64(4), "a", int64(2)}}, true},
			{`SELECT upper(json_object_keys('{"a":1,"b":2}')) AS k`, []string{"k"}, []string{"TEXT"}, [][]any{{"A"}, {"B"}}, false},
			{`SELECT json_object_keys(json_object_keys('{"{\"a\":1}":0,"{\"b\":2}":0}'::json)::json)`, []string{"json_object_keys"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}, false},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, tc.columns, tc.types, tc.rows)
				s.Bag = !tc.ordered
				fixture.JSONKeysBoth(t, p, b, c, s)
			})
		}
	})
}
