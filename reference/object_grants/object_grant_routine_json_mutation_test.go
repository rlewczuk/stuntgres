package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineJSONMutation(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.jsonb_set(jsonb,text[],jsonb,boolean)", "pg_catalog.jsonb_set_lax(jsonb,text[],jsonb,boolean,text)"})
		queries := []string{
			`SELECT jsonb_set('{"a":1}'::jsonb,'{a}'::text[],'2'::jsonb) AS v`,
			`SELECT jsonb_set('{"a":1}'::jsonb,'{a}'::text[],'2'::jsonb,true) AS v`,
			`SELECT jsonb_set(replacement=>'2'::jsonb,path=>'{a}'::text[],jsonb_in=>'{"a":1}'::jsonb) AS v`,
		}
		lax := `SELECT jsonb_set_lax('{"a":1}'::jsonb,'{a}'::text[],NULL::jsonb) AS v`
		query := `SELECT jsonb_set($1::jsonb,$2::text[],$3::jsonb) AS v`
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		want := fixture.Select(query, []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": 2}`}}, `{"a":1}`, `{a}`, `2`)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range queries {
				run(fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": 2}`}}))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Select(lax, []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": null}`}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.jsonb_set(JSONB,TEXT[],JSONB,BOOLEAN) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, q := range queries {
				fixture.RoleGrantPermissionDenied(run, q, "function", "jsonb_set")
			}
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function jsonb_set"}, `{"a":1}`, `{a}`, `2`)
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			run(fixture.Select(`SELECT jsonb_set(NULL::jsonb,'{a}'::text[],'2'::jsonb) AS v`, []string{"v"}, []string{"JSONB"}, [][]any{{nil}}))
			run(fixture.Select(lax, []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": null}`}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.jsonb_set_lax(JSONB,TEXT[],JSONB,BOOLEAN,TEXT) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, lax, "function", "jsonb_set_lax") })
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.jsonb_set(JSONB,TEXT[],JSONB,BOOLEAN),pg_catalog.jsonb_set_lax(JSONB,TEXT[],JSONB,BOOLEAN,TEXT) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Select(lax, []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": null}`}}))
		})
	})
}
