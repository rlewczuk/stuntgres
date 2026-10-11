package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func regexpNamespace(t *testing.T) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "regexp_empty")
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE SCHEMA regexp_empty`, 0))
		// A same-spelled relation is not function authority; qualified public calls
		// must not silently redirect to a pg_catalog builtin.
		run(fixture.Exec(`CREATE TABLE public.regexp_like(s TEXT)`, 0))
		const q = `SELECT regexp_like($1,'a') AS result`
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for _, path := range []string{"regexp_empty", "regexp_empty,pg_catalog", "pg_catalog,public", "public,pg_catalog"} {
			run(fixture.Exec("SET search_path="+path, 0))
			for _, prepared := range []bool{false, true} {
				for _, expr := range []string{`regexp_like('a','a')`, `pg_catalog.regexp_like('a','a')`} {
					s := regexpOne(b, expr, "BOOL", true)
					s.Prepared = prepared
					run(s)
				}
				errStep := regexpBindError(`SELECT public.regexp_like('a','a')`, "42883", `function public.regexp_like(unknown, unknown) does not exist`, similarFunctionHint, "public")
				errStep.Prepared = prepared
				if !prepared {
					errStep.ErrorStage = "execute"
				}
				run(errStep)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, q, []string{"result"}, []string{"BOOL"}, [][]any{{true}}, "a"))
		}
		run(fixture.Exec(`SET search_path=public`, 0))
		for _, prepared := range []bool{false, true} {
			s := regexpBindError(`SELECT /* é🙂 */ nonexistent.regexp_like('a','a')`, "3F000", `schema "nonexistent" does not exist`, "", "nonexistent")
			s.Prepared = prepared
			if !prepared {
				s.ErrorStage = "execute"
			}
			run(s)
		}
	})
}
