package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsPrepared(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = recorder
		run := func(s fixture.Step) { dsRun(t, p, b, c, route, s) }
		run(fixture.Exec("SET application_name='prepare-value'", 0))
		q := "ALTER DATABASE ds_one SET application_name FROM CURRENT"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		if len(notices) != 0 {
			t.Fatalf("Prepare emitted notices: %#v", notices)
		}
		execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		// One physical statement, not a succession of freshly prepared utilities.
		run(dsRow("ds_one", ""))
		dsFresh(t, p, b, route, "ds_one", "", "")
		run(fixture.Exec("SET application_name='execute-value'", 0))
		execute(fixture.Exec(q, 0))
		run(dsRow("ds_one", "{application_name=execute-value}"))
		run(fixture.Exec("SET ROLE ds_a", 0))
		execute(dsError(q, "42501", "must be owner of database ds_one"))
		run(fixture.Exec("RESET ROLE", 0))
		run(dsRow("ds_one", "{application_name=execute-value}"))
		run(fixture.Exec("SET ROLE ds_owner", 0))
		run(fixture.Exec("SET application_name='owner-value'", 0))
		execute(fixture.Exec(q, 0))
		run(fixture.Exec("RESET ROLE", 0))
		// The held statement must re-resolve real database ownership, not merely
		// the current actor. OWNER is existing integrated ownership behavior.
		dsRun(t, p, b, b.DB, route, fixture.Exec("ALTER DATABASE ds_one OWNER TO ds_b", 0))
		run(fixture.Exec("SET ROLE ds_owner", 0))
		execute(dsError(q, "42501", "must be owner of database ds_one"))
		run(fixture.Exec("RESET ROLE", 0))
		run(dsRow("ds_one", "{application_name=owner-value}"))
		dsRun(t, p, b, b.DB, route, fixture.Exec("ALTER DATABASE ds_one OWNER TO ds_owner", 0))
		dsFresh(t, p, b, route, "ds_one", "", "owner-value")
		// Prepare has no parameter authority or registry-validation side effects.
		run(fixture.Exec("SET ROLE ds_owner", 0))
		protected := "ALTER DATABASE ds_one SET log_statement_stats=off"
		ps, err := c.PrepareContext(p.Ctx, protected)
		if err != nil {
			t.Fatal(err)
		}
		defer ps.Close()
		run(dsRow("ds_one", "{application_name=owner-value}"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER log_statement_stats TO ds_owner", 0))
		run(fixture.Exec("SET ROLE ds_owner", 0))
		ph := fixture.HeldStatement{Executor: c, Statement: ps, SQL: protected}
		fixture.RunStep(t, p.Ctx, ph, b.Recorder, fixture.Exec(protected, 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER log_statement_stats FROM ds_owner", 0))
		run(fixture.Exec("SET ROLE ds_owner", 0))
		fixture.RunStep(t, p.Ctx, ph, b.Recorder, dsError(protected, "42501", `permission denied to set parameter "log_statement_stats"`))
		run(fixture.Exec("RESET ROLE", 0))
		run(dsRow("ds_one", "{application_name=owner-value,log_statement_stats=off}"))
		run(rsDatabaseApplication("owner-value"))
		if len(notices) != 0 {
			t.Fatalf("settings emitted unexpected notices: %#v", notices)
		}
		dsFresh(t, p, b, route, "ds_one", "", "owner-value")
	})
}

func TestReferenceDatabaseSettingsTransactions(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		c := dsConn(t, p, b)
		peer, _ := dsPool(t, p, b, "ds_two", "")
		run := func(s fixture.Step) { dsRun(t, p, b, c, route, s) }
		observe := func(value string) {
			dsRun(t, p, peer, peer.DB, route, dsRow("ds_one", "{application_name="+value+"}"))
			dsFresh(t, p, b, route, "ds_one", "", value)
			dsFresh(t, p, b, route, "ds_two", "", "peer")
		}
		run(fixture.Exec("ALTER DATABASE ds_two SET application_name='peer'", 0))
		run(fixture.Exec("ALTER DATABASE ds_one SET application_name='D'", 0))
		old, _ := dsPool(t, p, b, "ds_one", "")
		old.DB.SetMaxOpenConns(1)
		dsRun(t, p, old, old.DB, route, dsSetting("D", "database"))
		run(fixture.Exec("SET application_name='issuer'", 0))
		for _, q := range []string{"BEGIN", "ALTER DATABASE ds_one SET application_name='E'", "SAVEPOINT child", "ALTER DATABASE ds_one SET application_name='suffix'"} {
			run(fixture.Exec(q, 0))
		}
		run(dsRow("ds_one", "{application_name=suffix}"))
		observe("D")
		run(rsDatabaseApplication("issuer"))
		run(fixture.Exec("ROLLBACK TO child", 0))
		run(dsRow("ds_one", "{application_name=E}"))
		run(fixture.Exec("RELEASE child", 0))
		run(fixture.Exec("COMMIT", 0))
		observe("E")
		dsRun(t, p, old, old.DB, route, fixture.Exec("SET application_name='local'", 0))
		dsRun(t, p, old, old.DB, route, fixture.Exec("RESET application_name", 0))
		dsRun(t, p, old, old.DB, route, dsSetting("D", "database"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		run(dsRow("ds_one", ""))
		observe("E")
		run(fixture.Exec("ROLLBACK", 0))
		observe("E")
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(dsError("ALTER DATABASE ds_one RESET ALL", "25006", "cannot execute ALTER DATABASE in a read-only transaction"))
		aborted := dsError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if route != "direct" {
			aborted.ErrorStage = "prepare"
		}
		run(aborted)
		run(fixture.Exec("ROLLBACK", 0))
		observe("E")
		for _, finish := range []string{"ROLLBACK", "COMMIT"} {
			gid := "ds_settings_" + route + "_" + finish
			fixture.PreparedReferenceCleanup(t, b, gid)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER DATABASE ds_one SET application_name='two-phase'", 0))
			run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
			observe("E")
			run(fixture.Exec(finish+" PREPARED '"+gid+"'", 0))
			value := "E"
			if finish == "COMMIT" {
				value = "two-phase"
			}
			observe(value)
		}
		run(rsDatabaseApplication("issuer"))
	})
}
