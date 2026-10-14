package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBExistsFeatureSmoke(t *testing.T) {
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jbe_smoke(id int,doc jsonb)", 0))
		run(fixture.Exec(`INSERT INTO jbe_smoke VALUES(1,'{"a":null}'),(2,'{"outer":{"a":1}}'),(3,'["a"]'),(4,'"a"'),(5,NULL)`, 5))
		check := fixture.Select("SELECT id,doc ? 'a' AS has_a FROM jbe_smoke ORDER BY id", []string{"id", "has_a"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), true}, {int64(4), true}, {int64(5), nil}})
		jsonbExistsBoth(t, p, b, c, check)
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`'{"a":1}'::jsonb ?| '{missing,a}'::text[]`, true},
			{`'{"a":1}'::jsonb ?& '{a,missing}'::text[]`, false},
			{`'{}'::jsonb ?| '{}'::text[]`, false}, {`'{}'::jsonb ?& '{}'::text[]`, true},
			{`NULL::jsonb ?& '{}'::text[]`, nil}, {`'{}'::jsonb ?& '{NULL}'::text[]`, true},
		} {
			jsonbExistsBoth(t, p, b, c, jsonbExistsSelect("SELECT "+tc.expr+" AS v", tc.want))
		}
		q := "SELECT $1::jsonb ?| $2 AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			doc  any
			want any
		}{{`{"a":1}`, true}, {`{"outer":{"a":1}}`, false}, {nil, nil}} {
			s := jsonbExistsSelect(q, tc.want, tc.doc, "{missing,a}")
			s.CheckMetadata = jsonbExistsMetadata
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		jsonbExistsBoth(t, p, b, c, jsonbExistsSignature(`SELECT '{}'::jsonb ? 1`, "jsonb ? integer", 20))
		stored := jsonbExistsStored(t, p, b, c)
		jsonbExistsControls(t, p, b, c, stored)
	})
}
