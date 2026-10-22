package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func routineAuthority(t *testing.T) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"routine_reader"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE ROLE routine_reader",
			"GRANT USAGE ON SCHEMA public TO routine_reader",
			"CREATE TABLE routine_private(x integer)",
			"CREATE FUNCTION routine_authorized() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_private$$",
			"GRANT SELECT ON routine_private TO routine_reader",
			"REVOKE EXECUTE ON FUNCTION routine_authorized() FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION routine_authorized() TO routine_reader")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_private VALUES (8)", 1))
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION routine_reader")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		q := "SELECT routine_authorized() AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		value := func() {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
		}
		value()
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION routine_authorized() FROM routine_reader", "SET SESSION AUTHORIZATION routine_reader")
		routineDiagnostic(t, p, c, q, "42501", "permission denied for function routine_authorized", "", true)
		var denied int64
		err = stmt.QueryRowContext(p.Ctx).Scan(&denied)
		if err == nil {
			t.Fatal("held statement bypassed current EXECUTE")
		}
		want, pg := routineExpectedDiagnostic(q, "42501", "permission denied for function routine_authorized", "")
		fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, PostgresDiagnostic: &pg, ErrorStage: "execute"}, "execute")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION routine_authorized() TO routine_reader", "REVOKE SELECT ON routine_private FROM routine_reader", "SET SESSION AUTHORIZATION routine_reader")
		routineDiagnostic(t, p, c, q, "42501", "permission denied for table routine_private", "", true)
		err = stmt.QueryRowContext(p.Ctx).Scan(&denied)
		if err == nil {
			t.Fatal("held statement bypassed invoker table privilege")
		}
		want, pg = routineExpectedDiagnostic(q, "42501", "permission denied for table routine_private", "")
		fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want, PostgresDiagnostic: &pg, ErrorStage: "execute"}, "execute")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON routine_private TO routine_reader", "SET SESSION AUTHORIZATION routine_reader")
		value()
	})
}

// PostgreSQL accepts these declarations. Native must reject the excluded syntax
// without installing any signature; no PostgreSQL failure is fabricated.
func routineExcludedDeclarations(t *testing.T) {
	t.Helper()
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ create, call string }{
			{"CREATE FUNCTION routine_named(a integer) RETURNS integer LANGUAGE SQL AS $$SELECT a$$", "SELECT routine_named(4) AS v"},
			{"CREATE FUNCTION routine_mode(IN integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "SELECT routine_mode(4) AS v"},
			{"CREATE FUNCTION routine_multi() RETURNS integer LANGUAGE SQL AS $$SELECT 1; SELECT 4$$", "SELECT routine_multi() AS v"},
		} {
			routineExec(t, p, b, c, tc.create)
			routineValue(t, p, b, c, tc.call, "v", "INT4", int64(4))
		}
		for _, tc := range []struct {
			create, call string
			value        int64
		}{
			{"CREATE OR REPLACE FUNCTION routine_replace(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "SELECT routine_replace(4) AS v", 4},
			{"CREATE FUNCTION routine_strict(integer) RETURNS integer LANGUAGE SQL STRICT AS $$SELECT $1$$", "SELECT routine_strict(4) AS v", 4},
			{"CREATE FUNCTION routine_security(integer) RETURNS integer LANGUAGE SQL SECURITY DEFINER AS $$SELECT $1$$", "SELECT routine_security(4) AS v", 4},
		} {
			if b.PG {
				routineExec(t, p, b, c, tc.create)
				routineValue(t, p, b, c, tc.call, "v", "INT4", tc.value)
			} else {
				_, err := c.ExecContext(p.Ctx, tc.create)
				if err == nil {
					t.Fatalf("excluded declaration accepted: %s", tc.create)
				}
				fixture.State(t, err, "0A000")
				_, err = c.QueryContext(p.Ctx, tc.call)
				if err == nil {
					t.Fatalf("excluded signature installed: %s", tc.create)
				}
				fixture.State(t, err, "42883")
			}
		}
	})
}
