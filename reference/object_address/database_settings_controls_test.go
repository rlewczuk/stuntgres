package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsControls(t *testing.T) {
	dsCases(t, dsControls)
}

// SQL setup/observations are shared; only the raw native controls lack a PG API.
// Also called by the feature smoke, after its database-default lifecycle witness.
func dsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
	t.Helper()
	run := func(s fixture.Step) { dsRun(t, p, b, b.DB, route, s) }
	for _, q := range []string{
		"ALTER DATABASE ds_one SET application_name='control-before'", "ALTER DATABASE ds_two SET application_name='control-peer'",
		"ALTER ROLE ds_a SET work_mem='2MB'",
	} {
		run(fixture.Exec(q, 0))
	}
	a, raw := dsPool(t, p, b, "ds_one", "")
	peer, _ := dsPool(t, p, b, "ds_two", "")
	a.DB.SetMaxOpenConns(1)
	peer.DB.SetMaxOpenConns(1)
	arun := func(s fixture.Step) { dsRun(t, p, a, a.DB, route, s) }
	prun := func(s fixture.Step) { dsRun(t, p, peer, peer.DB, route, s) }
	var databaseOID, roleOID string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_database WHERE datname='ds_one'").Scan(&databaseOID); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_roles WHERE rolname='ds_a'").Scan(&roleOID); err != nil {
		t.Fatal(err)
	}
	arun(dsSetting("control-before", "database"))
	arun(fixture.Exec("CREATE TABLE public.ds_control(v integer)", 0))
	arun(fixture.Exec("INSERT INTO public.ds_control VALUES(42)", 1))
	prun(fixture.Exec("CREATE TABLE public.ds_control(v integer)", 0))
	prun(fixture.Exec("INSERT INTO public.ds_control VALUES(99)", 1))
	run(fixture.Exec("CREATE TABLE ds_default_sentinel(v integer)", 0))
	run(fixture.Exec("INSERT INTO ds_default_sentinel VALUES(7)", 1))
	values := func(value int64) fixture.Step {
		return fixture.Select("SELECT v FROM public.ds_control", []string{"v"}, []string{"INT4"}, [][]any{{value}})
	}
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, raw, &dump); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"ALTER DATABASE", "ALTER ROLE", "pg_db_role_setting", "control-before", "control-peer"} {
			if strings.Contains(dump.String(), forbidden) {
				t.Fatalf("dump carries cluster authority: %s", forbidden)
			}
		}
		// Keep eager Rows and this physical session's captured reset image alive.
		retained, err := a.DB.QueryContext(p.Ctx, "SELECT v FROM public.ds_control")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		run(fixture.Exec("ALTER DATABASE ds_one SET application_name='control-destination'", 0))
		if err := stuntgres.Reset(p.Ctx, raw); err != nil {
			t.Fatal(err)
		}
		// Close retained rows before borrowing the single-connection SQL pool.
		if !retained.Next() {
			t.Fatal("retained row disappeared", retained.Err())
		}
		var v int
		if err := retained.Scan(&v); err != nil || v != 42 {
			t.Fatal("retained snapshot", v, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained snapshot suffix", retained.Err())
		}
		retained.Close()
		arun(fixture.Select("SELECT relname FROM pg_class WHERE relname='ds_control'", []string{"relname"}, []string{"NAME"}, nil))
		arun(fixture.Exec("SET application_name='override'", 0))
		arun(fixture.Exec("RESET application_name", 0))
		arun(dsSetting("control-before", "database"))
		dsFresh(t, p, b, route, "ds_one", "", "control-destination")
		// A hostile destination default must not redirect private replay.
		run(fixture.Exec("ALTER DATABASE ds_one SET search_path='nonexistent'", 0))
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		arun(values(42))
		late := "CREATE TABLE public.ds_discard(v integer); CREATE SEQUENCE public.ds_bad CACHE 0;"
		want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, raw, strings.NewReader(late)), &want)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, raw, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore: %v", err)
		}
		if err := stuntgres.Reset(ctx, raw); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Reset: %v", err)
		}
		arun(values(42))
		arun(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('ds_discard','ds_bad')", []string{"relname"}, []string{"NAME"}, nil))
		run(dsRow("ds_one", "{application_name=control-destination,search_path=nonexistent}"))
		dsFresh(t, p, b, route, "ds_one", "", "control-destination")
		arun(dsSetting("control-before", "database"))
		arun(fixture.Select("SELECT current_database()", []string{"current_database"}, []string{"NAME"}, [][]any{{"ds_one"}}))
		run(fixture.Select("SELECT s.setconfig FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname='ds_a' AND s.setdatabase=0", []string{"setconfig"}, []string{"_TEXT"}, [][]any{{"{work_mem=2MB}"}}))
	}
	arun(values(42))
	run(fixture.Select("SELECT oid=$1::oid AS same_identity,pg_get_userbyid(datdba) AS owner FROM pg_database WHERE datname='ds_one'", []string{"same_identity", "owner"}, []string{"BOOL", "NAME"}, [][]any{{true, "ds_owner"}}, databaseOID))
	run(fixture.Select("SELECT oid=$1::oid AS same_identity FROM pg_roles WHERE rolname='ds_a'", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}, roleOID))
	prun(values(99))
	prun(dsSetting("control-peer", "database"))
	run(dsRow("ds_two", "{application_name=control-peer}"))
	run(fixture.Select("SELECT v FROM ds_default_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceDatabaseSettingsIsolation(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		run := func(s fixture.Step) { dsRun(t, p, b, b.DB, route, s) }
		run(fixture.Exec("ALTER DATABASE ds_one SET application_name='selected'", 0))
		a, raw := dsPool(t, p, b, "ds_one", "")
		dsRun(t, p, a, a.DB, route, dsSetting("selected", "database"))
		a.DB.Close()
		raw.Close()
		dsFresh(t, p, b, route, "ds_one", "", "selected")
		dsFresh(t, p, b, route, "ds_two", "", "")
		missing, _ := dsPool(t, p, b, "ds_missing", "")
		want := fixture.ControlsReferenceDiagnostic("3D000", `database "ds_missing" does not exist`)
		want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
		fixture.AssertDiagnostic(t, missing.DB.PingContext(p.Ctx), &want)
		run(fixture.Select("SELECT datname FROM pg_database WHERE datname='ds_missing'", []string{"datname"}, []string{"NAME"}, nil))
		if b.PG {
			return
		}
		empty, _ := dsPool(t, p, b, "", "")
		want.Message = `database "" does not exist`
		fixture.AssertDiagnostic(t, empty.DB.PingContext(p.Ctx), &want)
		owner := p.NativeConnector.Driver().(*stuntgres.Driver)
		// Exact keys are identities, not URLs or clipped current_database names.
		values := []string{"empty-key", "space-key", "url-key", "collision-a", "collision-b"}
		keys := []string{"", " opaque database settings ", "postgres://user@host/db?x=1", strings.Repeat("k", 63) + "a", strings.Repeat("k", 63) + "b"}
		for i, key := range keys {
			base, err := owner.OpenConnector(key)
			if err != nil {
				t.Fatal(err)
			}
			db, rawKey, record := fixture.DatabaseConnectorScenarioPool(t, base, false)
			dsRun(t, p, b, db, route, fixture.Exec("CREATE DATABASE ds_one", 0))
			fixture.RunStep(t, p.Ctx, db, record, dsRow("ds_one", ""))
			value := values[i]
			// Bind values are not part of ALTER DATABASE syntax; quote the literal.
			fixture.RunStep(t, p.Ctx, db, record, fixture.Exec("ALTER DATABASE ds_one SET application_name='"+strings.ReplaceAll(value, "'", "''")+"'", 0))
			db.Close()
			rawKey.Close()
			selected, err := owner.OpenDatabaseConnector(key, "ds_one")
			if err != nil {
				t.Fatal(err)
			}
			reopened, rawSelected, selectedRecord := fixture.DatabaseConnectorScenarioPool(t, selected, false)
			fixture.RunStep(t, p.Ctx, reopened, selectedRecord, rsDatabaseApplication(value))
			reopened.Close()
			rawSelected.Close()
		}
		for i, key := range keys {
			selected, err := owner.OpenDatabaseConnector(key, "ds_one")
			if err != nil {
				t.Fatal(err)
			}
			db, rawKey, record := fixture.DatabaseConnectorScenarioPool(t, selected, false)
			fixture.RunStep(t, p.Ctx, db, record, rsDatabaseApplication(values[i]))
			db.Close()
			rawKey.Close()
		}
		other := new(stuntgres.Driver)
		base, err := other.OpenConnector(p.NativeDSN)
		if err != nil {
			t.Fatal(err)
		}
		db, _, record := fixture.DatabaseConnectorScenarioPool(t, base, false)
		fixture.RunStep(t, p.Ctx, db, record, fixture.Exec("CREATE DATABASE ds_one", 0))
		fixture.RunStep(t, p.Ctx, db, record, dsRow("ds_one", ""))
		// WithDatabase is a copy and cannot retarget a live selected connector.
		original := p.NativeConnector.WithDatabase("ds_one")
		live, _, liveRecord := fixture.DatabaseConnectorScenarioPool(t, original, false)
		fixture.RunStep(t, p.Ctx, live, liveRecord, rsDatabaseApplication("selected"))
		copyPool, _, copyRecord := fixture.DatabaseConnectorScenarioPool(t, original.WithDatabase("ds_two"), false)
		fixture.RunStep(t, p.Ctx, copyPool, copyRecord, rsDatabaseApplication(""))
		fixture.RunStep(t, p.Ctx, live, liveRecord, rsDatabaseApplication("selected"))
	})
}
