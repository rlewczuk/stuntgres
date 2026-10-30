package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRecoveryControlNativeControls(t *testing.T) {
	const role = "recovery_control_restore_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer recoveryControlRestore(t, c, role, b.PG)()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE VIEW recovery_control_view AS SELECT pg_promote(wait_seconds=>NULL,wait=>false) AS promoted", 0))
		run(fixture.Exec("GRANT SELECT ON recovery_control_view TO "+role, 0))
		recoveryControlRoutes(t, p, b, c, recoveryControlNull("SELECT promoted FROM recovery_control_view"))
		for _, id := range []string{"pg_promote(boolean,integer)", "pg_wal_replay_pause()"} { run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO "+role, 0)) }
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_is_wal_replay_paused() FROM PUBLIC", 0))
		check := func(conn *sql.Conn, recorder *fixture.Recorder, rawNull bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(recoveryControlInitialPrivileges(b.PG))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(recoveryControlNull("SELECT promoted FROM recovery_control_view"))
			for _, name := range []string{"pg_promote", "pg_wal_replay_pause", "pg_get_wal_replay_pause_state"} { run(recoveryControlError("SELECT "+name+"()")) }
			run(recoveryControlDenied("SELECT pg_is_wal_replay_paused()", "pg_is_wal_replay_paused"))
			s := recoveryControlDenied("SELECT pg_wal_replay_resume()", "pg_wal_replay_resume")
			if rawNull { s = recoveryControlError("SELECT pg_wal_replay_resume()") }
			run(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT proacl IS NULL AS absent FROM pg_proc WHERE oid=3072", []string{"absent"}, []string{"BOOL"}, [][]any{{rawNull}}))
		}
		check(c, b.Recorder, false)
		if b.PG { return } // Native catalog replacement has no SQL oracle counterpart.
		// Raw catalog ACL replacement belongs to isolated Restore, not ordinary
		// SQL catalog mutation. Feed its existing lossless repair shape through
		// that public API, then re-Dump to witness raw-NULL preservation.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		dump.WriteString("\nUPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_wal_replay_resume()'::pg_catalog.regprocedure;\n")
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		check(c, b.Recorder, true)
		dump.Reset()
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		connector, err := stuntgres.NewDriver().OpenConnector("recovery_control_destination")
		if err != nil { t.Fatal(err) }
		control := sql.OpenDB(connector)
		defer control.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		if _, err := control.ExecContext(p.Ctx, "CREATE ROLE "+role); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		destination, err := recorded.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer destination.Close()
		check(destination, recorder, true)
		if err := stuntgres.Reset(p.Ctx, control); err != nil { t.Fatal(err) }
		runDestination := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, destination, recorder, s) }
		runDestination(recoveryControlACLStep(role, false))
		runDestination(recoveryControlInitialPrivileges(false))
		runDestination(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for i, name := range recoveryControlNames {
			s := recoveryControlError("SELECT "+name+"()")
			if i >= 2 { s = recoveryControlDenied("SELECT "+name+"()", name) }
			runDestination(s)
		}
		runDestination(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
