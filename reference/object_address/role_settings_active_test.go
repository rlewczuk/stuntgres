package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsSpecialRoleActiveState(t *testing.T) {
	rsActionCases(t, []string{"rs_f1_login", "rs_f1_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn, route string) {
		a := func(s fixture.Step) { rsActionRun(t, p, b, admin, route, s) }
		for _, q := range []string{
			"CREATE ROLE rs_f1_login LOGIN PASSWORD 'role-attributes-fixture'",
			"CREATE ROLE rs_f1_owner",
			"GRANT USAGE ON SCHEMA public TO rs_f1_login,rs_f1_owner",
			"CREATE TABLE rs_f1_capture(v text)",
			"GRANT INSERT ON rs_f1_capture TO rs_f1_owner",
			"CREATE FUNCTION rs_f1_read() RETURNS text LANGUAGE SQL SECURITY DEFINER AS $$INSERT INTO public.rs_f1_capture SELECT current_setting('role');SELECT current_user::text$$",
			"ALTER FUNCTION rs_f1_read() OWNER TO rs_f1_owner",
			"CREATE FUNCTION rs_f1_denied() RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN ALTER ROLE rs_f1_owner SET role FROM CURRENT; END$$",
			"ALTER FUNCTION rs_f1_denied() OWNER TO rs_f1_owner",
		} {
			a(fixture.Exec(q, 0))
		}
		pool := fixture.RoleAttributesPool(t, p, b, "rs_f1_login")
		c, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		exec := func(q string) { run(fixture.Exec(q, 0)) }
		read := func(role string) {
			run(fixture.Select("SHOW role", []string{"role"}, []string{"TEXT"}, [][]any{{role}}))
			run(fixture.Select("SELECT current_setting('role'),session_user::text,current_user::text", []string{"current_setting", "session_user", "current_user"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{role, "rs_f1_login", "rs_f1_login"}}))
		}
		definer := func(role string) {
			run(fixture.Select("SELECT rs_f1_read() AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"rs_f1_owner"}}))
			a(fixture.Select("SELECT v FROM rs_f1_capture", []string{"v"}, []string{"TEXT"}, [][]any{{role}}))
			a(fixture.Exec("DELETE FROM rs_f1_capture", 1))
			read(role)
		}
		read("none")
		definer("none")
		capture := fixture.Exec("ALTER ROLE rs_f1_login SET role FROM CURRENT", 0)
		captureCurrent := func() { run(capture) }
		if route == "held" {
			stmt, err := c.PrepareContext(p.Ctx, capture.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: capture.Query}
			captureCurrent = func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, capture) }
		}
		exec("SET ROLE rs_f1_login")
		read("rs_f1_login")
		captureCurrent()
		a(rsActionConfig("rs_f1_login", "{role=rs_f1_login}"))
		definer("rs_f1_login")
		// REL_18_6 guc.c rejects role validation even with source PGC_S_TEST.
		s := rsActionError("SELECT rs_f1_denied()", "42501", `cannot set parameter "role" within security-definer function`)
		s.SelectRows = true
		s.Diagnostic.Context = "SQL statement \"ALTER ROLE rs_f1_owner SET role FROM CURRENT\"\nPL/pgSQL function rs_f1_denied() line 1 at SQL statement"
		run(s)
		a(rsActionConfig("rs_f1_owner", ""))
		read("rs_f1_login")
		// Identity stays self throughout; only the active setting changes.
		exec("BEGIN")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		exec("SET LOCAL ROLE NONE")
		read("none")
		exec("SAVEPOINT f1")
		exec("SET LOCAL ROLE rs_f1_login")
		read("rs_f1_login")
		exec("ROLLBACK TO f1")
		read("none")
		exec("COMMIT")
		read("rs_f1_login")
		exec("BEGIN")
		exec("SET ROLE NONE")
		read("none")
		exec("ROLLBACK")
		read("rs_f1_login")
		freshPool := fixture.RoleAttributesPool(t, p, b, "rs_f1_login")
		fresh, err := freshPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		c = fresh
		read("rs_f1_login")
		// Persisted mutation cannot replace this physical session's reset image.
		a(fixture.Exec("ALTER ROLE rs_f1_login RESET role", 0))
		exec("SET ROLE NONE")
		read("none")
		exec("RESET ROLE")
		read("rs_f1_login")
	})
}
