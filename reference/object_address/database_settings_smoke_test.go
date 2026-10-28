package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsFeatureSmoke(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		owner, _ := dsPool(t, p, b, "ds_two", "ds_owner")
		issuer := dsConn(t, p, owner)
		run := func(s fixture.Step) { dsRun(t, p, owner, issuer, route, s) }
		oldA, _ := dsPool(t, p, b, "ds_one", "")
		oldB, _ := dsPool(t, p, b, "ds_two", "")
		oldA.DB.SetMaxOpenConns(1)
		oldB.DB.SetMaxOpenConns(1)
		dsRun(t, p, oldA, oldA.DB, route, rsDatabaseApplication(""))
		dsRun(t, p, oldB, oldB.DB, route, rsDatabaseApplication(""))
		run(fixture.Select("SELECT rolcreatedb FROM pg_roles WHERE rolname='ds_owner'", []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{false}}))
		q := "ALTER DATABASE ds_one SET application_name='database-smoke'"
		stmt, err := issuer.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(dsRow("ds_one", ""))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: issuer, Statement: stmt, SQL: q}, owner.Recorder, fixture.Exec(q, 0))
		run(dsRow("ds_one", "{application_name=database-smoke}"))
		run(rsDatabaseApplication(""))
		dsRun(t, p, oldA, oldA.DB, route, rsDatabaseApplication(""))
		dsRun(t, p, oldB, oldB.DB, route, rsDatabaseApplication(""))
		freshA, _ := dsPool(t, p, b, "ds_one", "")
		freshA.DB.SetMaxOpenConns(1)
		dsRun(t, p, freshA, freshA.DB, route, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"database-smoke"}}))
		dsRun(t, p, freshA, freshA.DB, route, dsSetting("database-smoke", "database"))
		dsRun(t, p, freshA, freshA.DB, route, fixture.Exec("SET application_name='local'", 0))
		dsRun(t, p, freshA, freshA.DB, route, fixture.Exec("RESET application_name", 0))
		dsRun(t, p, freshA, freshA.DB, route, rsDatabaseApplication("database-smoke"))
		dsFresh(t, p, b, route, "ds_two", "", "")
		capture := "ALTER DATABASE ds_one SET application_name FROM CURRENT"
		held, err := issuer.PrepareContext(p.Ctx, capture)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(fixture.Exec("SET application_name='captured-smoke'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: issuer, Statement: held, SQL: capture}, owner.Recorder, fixture.Exec(capture, 0))
		run(dsRow("ds_one", "{application_name=captured-smoke}"))
		dsFresh(t, p, b, route, "ds_one", "", "captured-smoke")
		dsRun(t, p, freshA, freshA.DB, route, rsDatabaseApplication("database-smoke"))
		run(dsError("ALTER DATABASE ds_one SET standard_conforming_strings='o'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`))
		other, _ := dsPool(t, p, b, "ds_two", "ds_b")
		dsRun(t, p, other, other.DB, route, dsError("ALTER DATABASE ds_one RESET ALL", "42501", "must be owner of database ds_one"))
		run(dsRow("ds_one", "{application_name=captured-smoke}"))
		run(rsDatabaseApplication("captured-smoke"))
		dsFresh(t, p, b, route, "ds_one", "", "captured-smoke")
		for _, sql := range []string{"BEGIN", "ALTER DATABASE ds_one SET application_name='rollback-smoke'", "ROLLBACK"} {
			run(fixture.Exec(sql, 0))
		}
		run(dsRow("ds_one", "{application_name=captured-smoke}"))
		dsFresh(t, p, b, route, "ds_one", "", "captured-smoke")
		run(fixture.Exec("ALTER DATABASE ds_one RESET application_name", 0))
		run(dsRow("ds_one", ""))
		dsFresh(t, p, b, route, "ds_one", "", "")
		dsRun(t, p, freshA, freshA.DB, route, fixture.Exec("RESET application_name", 0))
		dsRun(t, p, freshA, freshA.DB, route, rsDatabaseApplication("database-smoke"))
		dsRun(t, p, oldB, oldB.DB, route, rsDatabaseApplication(""))
		dsControls(t, p, b, route)
	})
}
