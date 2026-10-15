package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jbcd_reader"})
	fixture.SchemaReferenceCleanup(t, "jbcd_private")
	fixture.SchemaReferenceCleanup(t, "jbcd_empty")
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE SCHEMA jbcd_empty", 0))
		for _, op := range []string{"-", "||"} {
			for _, schema := range []string{"jbcd_missing", "jbcd_empty"} {
				rhs, typ := "'a'::text", "text"
				if op == "||" {
					rhs, typ = "'{}'::jsonb", "jsonb"
				}
				d := fixture.ControlsReferenceDiagnostic("3F000", `schema "jbcd_missing" does not exist`)
				if schema == "jbcd_empty" {
					d = fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: jsonb jbcd_empty."+op+" "+typ)
					d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				}
				d.Position = "20"
				jsonTypeAnalysisError(t, p, b, c, jsonbConcatDeleteError("SELECT '{}'::jsonb OPERATOR("+schema+"."+op+") "+rhs, d))
			}
		}
		run(fixture.Exec("CREATE USER jbcd_reader", 0))
		run(fixture.Exec("CREATE SCHEMA jbcd_private", 0))
		run(fixture.Exec("CREATE TABLE jbcd_acl(doc jsonb,rhs jsonb,key text,idx int)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO jbcd_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema jbcd_private")
		d.Position = "20"
		jsonTypeAnalysisError(t, p, b, c, jsonbConcatDeleteError(`SELECT NULL::jsonb OPERATOR(jbcd_private.-) 'a'::text`, d))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, tc := range []struct{ rhs, op, null, want string }{
			{"rhs", "||", "jsonb", `[1, 2, 3]`}, {"key", "-", "text", `[1, 2]`}, {"idx", "-", "integer", `[1]`},
		} {
			t.Run(tc.rhs, func(t *testing.T) {
				deny := func(q string) {
					jsonTypeBoth(t, p, b, c, jsonbConcatDeleteError(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbcd_acl")))
				}
				run(fixture.Exec("GRANT SELECT("+tc.rhs+") ON jbcd_acl TO jbcd_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
				deny("SELECT doc " + tc.op + " " + tc.rhs + " FROM jbcd_acl")
				deny("SELECT doc " + tc.op + " NULL::" + tc.null + " FROM jbcd_acl WHERE FALSE")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE SELECT("+tc.rhs+") ON jbcd_acl FROM jbcd_reader", 0))
				run(fixture.Exec("GRANT SELECT(doc) ON jbcd_acl TO jbcd_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
				deny("SELECT NULL::jsonb " + tc.op + " " + tc.rhs + " FROM jbcd_acl WHERE FALSE")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec(`INSERT INTO jbcd_acl VALUES('[1,2]','3','missing',-1)`, 1))
				run(fixture.Exec("GRANT SELECT("+tc.rhs+") ON jbcd_acl TO jbcd_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
				q := "SELECT doc " + tc.op + " " + tc.rhs + " AS v FROM jbcd_acl"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				good := jsonbConcatDeleteSelect(q, tc.want)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE SELECT("+tc.rhs+") ON jbcd_acl FROM jbcd_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbConcatDeleteError(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbcd_acl")))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT SELECT("+tc.rhs+") ON jbcd_acl TO jbcd_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION jbcd_reader", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE SELECT(doc,"+tc.rhs+") ON jbcd_acl FROM jbcd_reader", 0))
				run(fixture.Exec("DELETE FROM jbcd_acl", 1))
			})
		}
	})
}
