package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jt_reader"})
	fixture.SchemaReferenceCleanup(t, "jt_private")
	jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
		run(fixture.Exec(`CREATE USER jt_reader`, 0))
		run(fixture.Exec(`CREATE SCHEMA jt_private`, 0))
		run(fixture.Exec(`CREATE TABLE jt_private.docs(doc jsonb)`, 0))
		run(fixture.Exec(`INSERT INTO jt_private.docs VALUES('[7]')`, 1))
		run(fixture.Exec(`CREATE FUNCTION jt_private.fallback() RETURNS int LANGUAGE SQL AS 'SELECT 9'`, 0))
		run(fixture.Exec(`REVOKE ALL ON FUNCTION jt_private.fallback() FROM PUBLIC`, 0))
		run(fixture.Exec(`GRANT USAGE ON SCHEMA public,jt_private TO jt_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
		q := `SELECT j.v FROM jt_private.docs d CROSS JOIN JSON_TABLE(d.doc,'$[*]' COLUMNS(v int PATH '$',unused int PATH '$.absent' DEFAULT jt_private.fallback() ON EMPTY)) j`
		// Native's generic expression admission checks DEFAULT routines before
		// executor table ACLs. PostgreSQL checks this default-volatility
		// statement's table first. Isolated denials below must still agree.
		bothDenied := "permission denied for function fallback"
		if b.PG {
			bothDenied = "permission denied for table docs"
		}
		sqljsonDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", bothDenied), false)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION jt_private.fallback() TO jt_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
		sqljsonDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table docs"), false)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT SELECT ON jt_private.docs TO jt_reader`, 0))
		run(fixture.Exec(`REVOKE EXECUTE ON FUNCTION jt_private.fallback() FROM jt_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
		sqljsonDiagnostic(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function fallback"), false)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT EXECUTE ON FUNCTION jt_private.fallback() TO jt_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		for _, tc := range []struct{ revoke, grant, message string }{
			{`REVOKE SELECT ON jt_private.docs FROM jt_reader`, `GRANT SELECT ON jt_private.docs TO jt_reader`, `permission denied for table docs`},
			{`REVOKE EXECUTE ON FUNCTION jt_private.fallback() FROM jt_reader`, `GRANT EXECUTE ON FUNCTION jt_private.fallback() TO jt_reader`, `permission denied for function fallback`},
		} {
			run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
			run(fixture.Exec(tc.revoke, 0))
			run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", tc.message))
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
			run(fixture.Exec(tc.grant, 0))
			run(fixture.Exec(`SET SESSION AUTHORIZATION jt_reader`, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		// Even never-produced, unprojected output declarations and DEFAULTs bind.
		for _, tc := range []struct {
			q, code, message string
			position         int
			hint             string
		}{
			{`SELECT 1 FROM JSON_TABLE('[]','$[*]' COLUMNS(v jt_missing))`, "42704", `type "jt_missing" does not exist`, 48, ""},
			{`SELECT 1 FROM JSON_TABLE(NULL::jsonb,'$' COLUMNS(v int DEFAULT jt_missing() ON EMPTY))`, "42883", `function jt_missing() does not exist`, 64, `No function matches the given name and argument types. You might need to add explicit type casts.`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fixture.Position(tc.position)
				d.Hint = tc.hint
				sqljsonDiagnostic(t, p, b, c, tc.q, d, true)
			})
		}
	})
}
