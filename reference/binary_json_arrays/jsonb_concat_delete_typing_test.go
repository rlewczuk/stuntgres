package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteTyping(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{`SELECT '{}'::jsonb || '{"a":1}' AS v`, "JSONB", `{"a": 1}`, nil},
			{`SELECT '{"a":1}' || '{}'::jsonb AS v`, "JSONB", `{"a": 1}`, nil},
			{`SELECT 'a' || 'b' AS v`, "TEXT", "ab", nil},
			{`SELECT 'x'::text || '{"a":1}'::jsonb AS v`, "TEXT", `x{"a": 1}`, nil},
			{`SELECT '{"a":1}'::jsonb || 'x'::text AS v`, "TEXT", `{"a": 1}x`, nil},
			{`SELECT '["0",1]'::jsonb - '0' AS v`, "JSONB", `[1]`, nil},
			{`SELECT $1::jsonb - $2 AS v`, "JSONB", `[1]`, []any{`["0",1]`, "0"}},
			{`SELECT '{"a":1}' - 'a'::text AS v`, "JSONB", `{}`, nil},
			{`SELECT '[1,2]'::jsonb - 0::smallint AS v`, "JSONB", `[2]`, nil},
			{`SELECT '3' - 1 AS v`, "INT4", int64(2), nil},
			{`SELECT 3 - 1 AS v`, "INT4", int64(2), nil},
			{`SELECT '[1,2]'::jsonb OPERATOR(pg_catalog.-) -1 AS v`, "JSONB", `[1]`, nil},
			{`SELECT '{}'::jsonb OPERATOR(pg_catalog.||) '{"a":1}' AS v`, "JSONB", `{"a": 1}`, nil},
			{`SELECT ('{"a":1}'::jsonb || '{"b":2}') - 'a' AS v`, "JSONB", `{"b": 2}`, nil},
			{`SELECT ('[1,2]'::jsonb - 0) || '3'::jsonb AS v`, "JSONB", `[2, 3]`, nil},
			{`SELECT '[1]'::jsonb - NULL AS v`, "JSONB", nil, nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				jsonTypeBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...))
			})
		}
		for _, q := range []string{"CREATE DOMAIN jbcd_doc AS jsonb", "CREATE DOMAIN jbcd_key AS text", "CREATE DOMAIN jbcd_index AS integer"} {
			jsonTypeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ q, want string }{
			{`SELECT '{"a":1}'::jbcd_doc - 'a'::jbcd_key AS v`, `{}`},
			{`SELECT '[1,2]'::jbcd_doc - -1::jbcd_index AS v`, `[1]`},
			{`SELECT '{}'::jbcd_doc || '{"a":1}'::jbcd_doc AS v`, `{"a": 1}`},
		} {
			jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect(tc.q, tc.want))
		}
	})
}

func TestReferenceJSONBConcatDeleteMetadata(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbcd_meta(id int,doc jsonb,rhs jsonb,key text,idx int)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbcd_meta VALUES(1,'[1,2]','3','missing',-1),(2,NULL,'3','missing',-1)`, 2))
		for _, expression := range []struct{ sql, want string }{{"doc || rhs", "[1, 2, 3]"}, {"doc - key", "[1, 2]"}, {"doc - idx", "[1]"}} {
			for _, tc := range []struct {
				suffix string
				rows   [][]any
			}{
				{" ORDER BY id", [][]any{{expression.want}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE FALSE", nil},
			} {
				q := "SELECT " + expression.sql + " AS v FROM jbcd_meta" + tc.suffix
				s := jsonbPathDeleteMetadata(fixture.Select(q, []string{"v"}, []string{"JSONB"}, tc.rows), b.PG)
				jsonTypeBoth(t, p, b, c, s)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				stmt.Close()
			}
		}
	})
}
