package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var recoveryControlIdentities = []string{"pg_is_wal_replay_paused()", "pg_get_wal_replay_pause_state()", "pg_promote(boolean,integer)", "pg_wal_replay_pause()", "pg_wal_replay_resume()"}

// Registered before mutation and run while the connection is still borrowed.
// Builtin ACLs are cluster state, not part of the fixture's public-schema reset.
func recoveryControlRestore(t *testing.T, c *sql.Conn, role string, pg bool) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil { t.Error(err); return }
		for i, identity := range recoveryControlIdentities {
			if _, err := c.ExecContext(ctx, "REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM "+role); err != nil { t.Error(err) }
			action := "REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM PUBLIC"
			if i < 2 { action = "GRANT EXECUTE ON FUNCTION pg_catalog."+identity+" TO PUBLIC" }
			if _, err := c.ExecContext(ctx, action); err != nil { t.Error(err) }
		}
		// PostgreSQL is shared across scenarios: restore raw absence too. Each
		// native fixture is isolated; catalog repairs there are Restore-only.
		if pg {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid IN (1137,3073)"); err != nil { t.Error(err) }
		}
	}
}
func recoveryControlDenied(query, name string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function "+name})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceRecoveryControlPrivileges(t *testing.T) {
	const role = "recovery_control_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer recoveryControlRestore(t, c, role, b.PG)()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE TABLE recovery_control_null(v boolean)", 0))
		run(fixture.Exec("INSERT INTO recovery_control_null VALUES(NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON recovery_control_null TO "+role, 0))
		statements := make([]*sql.Stmt, 5)
		for i, name := range recoveryControlNames {
			stmt, err := c.PrepareContext(p.Ctx, "SELECT "+name+"()")
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			statements[i] = stmt
		}
		heldRun := func(i int, s fixture.Step) { fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statements[i], SQL: "SELECT "+recoveryControlNames[i]+"()"}, b.Recorder, s) }
		reader := func() { run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0)) }
		admin := func() { run(fixture.Exec("RESET SESSION AUTHORIZATION", 0)) }
		reader()
		for i, name := range recoveryControlNames {
			q := "SELECT "+name+"()"
			s := recoveryControlError(q)
			if i >= 2 { s = recoveryControlDenied(q, name) }
			heldRun(i, s)
			run(s)
			run(fixture.Prepared(s))
		}
		// A constant NULL eliminates the invocation. A table NULL does not.
		recoveryControlRoutes(t, p, b, c, recoveryControlNull("SELECT pg_promote(NULL) AS promoted"))
		runtimeNull := "SELECT pg_promote(v) AS promoted FROM recovery_control_null"
		run(recoveryControlDenied(runtimeNull, "pg_promote"))
		admin()
		for i := 2; i < 5; i++ { run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+recoveryControlIdentities[i]+" TO "+role, 0)) }
		reader()
		for i := 2; i < 5; i++ { heldRun(i, recoveryControlError("SELECT "+recoveryControlNames[i]+"()")) }
		recoveryControlRoutes(t, p, b, c, recoveryControlNull(runtimeNull))
		admin()
		run(recoveryControlACLStep(role, true))
		run(recoveryControlInitialPrivileges(b.PG))
		for i := 2; i < 5; i++ { run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+recoveryControlIdentities[i]+" FROM "+role, 0)) }
		run(recoveryControlACLStep(role, false))
		run(recoveryControlInitialPrivileges(b.PG))
		reader()
		for i := 2; i < 5; i++ { heldRun(i, recoveryControlDenied("SELECT "+recoveryControlNames[i]+"()", recoveryControlNames[i])) }
		admin()
		for i := 2; i < 5; i++ { heldRun(i, recoveryControlError("SELECT "+recoveryControlNames[i]+"()")) }
		for i := range 2 {
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+recoveryControlIdentities[i]+" FROM PUBLIC", 0))
			reader()
			heldRun(i, recoveryControlDenied("SELECT "+recoveryControlNames[i]+"()", recoveryControlNames[i]))
			admin()
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+recoveryControlIdentities[i]+" TO PUBLIC", 0))
			reader()
			heldRun(i, recoveryControlError("SELECT "+recoveryControlNames[i]+"()"))
			admin()
		}
	})
}

func recoveryControlACLStep(role string, granted bool) fixture.Step {
	return fixture.Select("SELECT has_function_privilege('"+role+"','pg_promote(boolean,integer)','EXECUTE') AS promote,has_function_privilege('"+role+"','pg_wal_replay_pause()','EXECUTE') AS pause,has_function_privilege('"+role+"','pg_wal_replay_resume()','EXECUTE') AS resume", []string{"promote", "pause", "resume"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{granted, granted, granted}})
}

func TestReferenceRecoveryControlFeatureSmoke(t *testing.T) {
	const role = "recovery_control_smoke_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer recoveryControlRestore(t, c, role, b.PG)()
		q := "SELECT pg_promote()"
		run(recoveryControlError(q))
		run(recoveryControlError("SELECT pg_promote(wait_seconds=>0,wait=>false)"))
		run(recoveryControlNull("SELECT pg_promote(NULL) AS promoted"))
		run(fixture.Select("SELECT COALESCE(pg_promote(NULL),false) AS promoted", []string{"promoted"}, []string{"BOOL"}, [][]any{{false}}))
		s := fixture.Select(recoveryControlProjection, recoveryControlNames, recoveryControlTypes, nil)
		s.CheckMetadata = recoveryControlMetadata(b.PG, recoveryControlNames)
		run(s)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, granted := range []bool{false, true, false} {
			action := "REVOKE EXECUTE ON FUNCTION pg_promote(boolean,integer) FROM "+role
			if granted { action = "GRANT EXECUTE ON FUNCTION pg_promote(boolean,integer) TO "+role }
			run(fixture.Exec(action, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			s := recoveryControlDenied(q, "pg_promote")
			if granted { s = recoveryControlError(q) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(recoveryControlError("SELECT pg_is_wal_replay_paused()"))
			recoveryControlPrimary(t, p, b, c)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
