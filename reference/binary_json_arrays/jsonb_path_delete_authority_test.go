package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPathDeleteAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"jbd_reader"})
	fixture.SchemaReferenceCleanup(t, "jbd_private")
	fixture.SchemaReferenceCleanup(t, "jbd_empty")
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE SCHEMA jbd_empty", 0))
		for _, tc := range []struct{ schema, code, msg, hint string }{
			{"jbd_missing", "3F000", `schema "jbd_missing" does not exist`, ""},
			{"jbd_empty", "42883", "operator does not exist: jsonb jbd_empty.#- text[]", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
			d.Position, d.Hint = "20", tc.hint
			jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError("SELECT '{}'::jsonb OPERATOR("+tc.schema+".#-) '{}'::text[]", d))
		}
		run(fixture.Exec("CREATE USER jbd_reader", 0))
		run(fixture.Exec("CREATE SCHEMA jbd_private", 0))
		run(fixture.Exec("CREATE TABLE jbd_acl(doc jsonb,path text[])", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO jbd_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema jbd_private")
		d.Position = "20"
		jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError(`SELECT NULL::jsonb OPERATOR(jbd_private.#-) '{}'::text[]`, d))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		deny := func(q string) {
			s := jsonbPathDeleteError(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbd_acl"))
			jsonTypeBoth(t, p, b, c, s)
		}
		run(fixture.Exec("GRANT SELECT(path) ON jbd_acl TO jbd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		deny("SELECT doc #- path FROM jbd_acl")
		deny("SELECT doc #- NULL::text[] FROM jbd_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(path) ON jbd_acl FROM jbd_reader", 0))
		run(fixture.Exec("GRANT SELECT(doc) ON jbd_acl TO jbd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		deny("SELECT doc #- path FROM jbd_acl")
		deny("SELECT NULL::jsonb #- path FROM jbd_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jbd_acl VALUES('{"a":1}','{a}')`, 1))
		run(fixture.Exec("GRANT SELECT(path) ON jbd_acl TO jbd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		q := "SELECT doc #- path AS v FROM jbd_acl"
		good := jsonbPathDeleteSelect(q, `{}`)
		jsonTypeBoth(t, p, b, c, good)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(path) ON jbd_acl FROM jbd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbPathDeleteError(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbd_acl")))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(path) ON jbd_acl TO jbd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION jbd_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
