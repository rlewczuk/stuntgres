package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWControls(t *testing.T) {
	provider := &fixture.ExternalSQLProvider{}
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return postgresFDWConfigure(d, provider, fixture.ExternalSQLFixtureVersion, true)
	})
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(user 'remote',password 'not-a-real-connection')", 0),
			fixture.Exec("CREATE FOREIGN TABLE pf_table(id integer OPTIONS(column_name 'Remote ID'),label text OPTIONS(column_name 'remote_label')) SERVER pf_server OPTIONS(schema_name 'quoted source',table_name 'quoted table',fetch_size '2')", 0),
			fixture.Exec("CREATE TABLE pf_local(v integer)", 0),
			fixture.Exec("INSERT INTO pf_local VALUES(73)", 1),
			fixture.Exec("CREATE SCHEMA pf_helpers", 0),
			fixture.Exec("CREATE SCHEMA pf_moved", 0),
			fixture.Exec("ALTER EXTENSION postgres_fdw SET SCHEMA pf_helpers", 0),
			fixture.Exec("ALTER FUNCTION pf_helpers.postgres_fdw_handler() RENAME TO actual_handler", 0),
			fixture.Exec("ALTER FUNCTION pf_helpers.postgres_fdw_validator(text[],oid) RENAME TO actual_validator", 0),
			fixture.Exec("ALTER FUNCTION pf_helpers.actual_validator(text[],oid) SET SCHEMA pf_moved", 0),
			fixture.Exec("ALTER FOREIGN DATA WRAPPER postgres_fdw RENAME TO pf_alias", 0),
			fixture.Exec("ALTER FOREIGN TABLE pf_table RENAME COLUMN id TO mapped_id", 0),
		} {
			run(s)
		}
		run(fixture.Exec("GRANT SELECT ON pf_table TO PUBLIC", 0))
		check := func(executor fixture.Executor, recorder *fixture.Recorder) {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}),
				fixture.Select("SELECT w.fdwhandler='pf_helpers.actual_handler()'::regprocedure AS handler,w.fdwvalidator='pf_moved.actual_validator(text[],oid)'::regprocedure AS validator,w.fdwowner=e.extowner AS owner FROM pg_foreign_data_wrapper w JOIN pg_extension e ON e.extname='postgres_fdw' WHERE w.fdwname='pf_alias'", []string{"handler", "validator", "owner"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}),
				fixture.Select("SELECT attname,attfdwoptions FROM pg_attribute WHERE attrelid='pf_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attfdwoptions"}, []string{"NAME", "_TEXT"}, [][]any{{"mapped_id", `{"column_name=Remote ID"}`}, {"label", "{column_name=remote_label}"}}),
				fixture.Select("SELECT ftoptions FROM pg_foreign_table WHERE ftrelid='pf_table'::regclass", []string{"ftoptions"}, []string{"_TEXT"}, [][]any{{relationOptionsArray([]string{"schema_name=quoted source", "table_name=quoted table", "fetch_size=2"})}}),
				fixture.Select("SELECT umoptions FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='pf_server')", []string{"umoptions"}, []string{"_TEXT"}, [][]any{{relationOptionsArray([]string{"user=remote", "password=not-a-real-connection"})}}),
				fixture.Select("SELECT c.relowner=s.srvowner AS same_owner,EXISTS(SELECT 1 FROM aclexplode(c.relacl) a WHERE a.grantee=0 AND a.privilege_type='SELECT' AND NOT a.is_grantable) AS public_select FROM pg_class c JOIN pg_foreign_server s ON s.srvname='pf_server' WHERE c.oid='pf_table'::regclass", []string{"same_owner", "public_select"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
				fileFDWVoid("SELECT pf_moved.actual_validator(ARRAY['fetch_size=1'],1417::oid) AS validated"),
				postgresFDWError("SELECT pf_moved.actual_validator(ARRAY['fetch_size=0'],1417::oid)", "22023", `"fetch_size" must be an integer value greater than zero`),
			} {
				fixture.RunStep(t, p.Ctx, executor, recorder, s)
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Prepared(s))
			}
		}
		check(c, b.Recorder)
		q := "SELECT pf_moved.actual_validator(ARRAY[$1::text],1417::oid) AS validated"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fileFDWVoid(q, "fetch_size=2"))
		// Explicit native controls have no fabricated PostgreSQL substitute.
		if b.PG {
			return
		}
		before := provider.OpenCalls()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if provider.OpenCalls() != before {
			t.Fatal("metadata Dump opened transport")
		}
		old, err := c.QueryContext(p.Ctx, "SELECT v FROM pf_local")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		if provider.OpenCalls() != before {
			t.Fatal("Reset/Restore opened transport")
		}
		var value int64
		if !old.Next() {
			t.Fatal("retained local rows disappeared", old.Err())
		}
		if err := old.Scan(&value); err != nil || value != 73 {
			t.Fatal("retained value", value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained row shape changed", old.Err())
		}
		check(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fileFDWVoid(q, "fetch_size=3"))
		postgresFDWDestinations(t, p, dump.String(), check)
	})
	t.Run("native_unbound_manifest", postgresFDWUnboundControls)
	if !*fixture.Mode {
		return
	}
	postgresFDWPhysicalControls(t)
}

func postgresFDWDestinations(t *testing.T, p *fixture.Pair, dump string, check func(fixture.Executor, *fixture.Recorder)) {
	t.Helper()
	for _, mode := range []string{"missing", "unbound", "wrong-version", "matching"} {
		t.Run(mode, func(t *testing.T) {
			d := stuntgres.NewDriver()
			provider := &fixture.ExternalSQLProvider{}
			if mode != "missing" {
				version := fixture.ExternalSQLFixtureVersion
				if mode == "wrong-version" {
					version = "wrong-version"
				}
				if err := postgresFDWConfigure(d, provider, version, mode != "unbound"); err != nil {
					t.Fatal(err)
				}
			}
			connector, err := d.OpenConnector("postgres-fdw-destination/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			db, recorder := fixture.RecordedNativePool(t, connector)
			defer raw.Close()
			defer db.Close()
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("CREATE TABLE destination_keep(v integer)", 0))
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO destination_keep VALUES(82)", 1))
			err = stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump))
			if mode != "matching" {
				if err == nil {
					t.Fatal("unauthentic destination accepted")
				}
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT v FROM destination_keep", []string{"v"}, []string{"INT4"}, [][]any{{int64(82)}}))
			} else {
				if err != nil {
					t.Fatal(err)
				}
				check(db, recorder)
				var marker string
				for _, line := range strings.Split(dump, "\n") {
					if strings.HasPrefix(line, "-- stuntgres-external-sql-v1 ") {
						marker = line + "\n"
						break
					}
				}
				if marker == "" {
					t.Fatal("bound postgres_fdw dump omitted prerequisite")
				}
				for _, invalid := range []string{marker + dump, strings.Replace(dump, marker, "", 1), strings.Replace(dump, marker, "-- stuntgres-external-sql-v1 malformed\n", 1)} {
					if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(invalid)); err == nil {
						t.Fatal("missing/malformed/duplicate prerequisite accepted")
					}
					check(db, recorder)
				}
			}
			if provider.OpenCalls() != 0 {
				t.Fatal("metadata replay demanded a physical resource")
			}
		})
	}
}

func postgresFDWPhysicalControls(t *testing.T) {
	t.Helper()
	p, provider := postgresFDWPhysicalScenario(t)
	b := externalSQLNativeBackend(p)
	c := externalSQLProviderConnection(t, p, b)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	postgresFDWPhysicalInstall(t, b, run)
	externalSQLProviderValues(t, p, b, c, "pf_table")
	pid := postgresFDWPID(t, p, c)
	old, err := c.QueryContext(p.Ctx, "SELECT id,label,flag,bytes FROM pf_table ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before := provider.OpenCalls()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if provider.OpenCalls() != before {
		t.Fatal("physical Dump opened connection")
	}
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted"."data table" SET remote_label='after-dump' WHERE "Remote ID"=1`, 1))
	// Failed and canceled replacements retain the actual incumbent connection.
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nSELECT missing_restore_name;")); err == nil {
		t.Fatal("bad replacement accepted")
	}
	if got := postgresFDWPID(t, p, c); got != pid {
		t.Fatal("failed Restore detached live handle")
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); err == nil {
		t.Fatal("canceled replacement accepted")
	}
	if got := postgresFDWPID(t, p, c); got != pid {
		t.Fatal("canceled Restore detached live handle")
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	postgresFDWObserveGone(t, p, pid)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	if provider.OpenCalls() != before {
		t.Fatal("Restore serialized/opened handles")
	}
	externalSQLOldRows(t, old)
	run(externalSQLProviderRows(false, "SELECT id,label,flag,bytes FROM pf_table WHERE id=1", [][]any{{int64(1), "after-dump", true, "\x00\xff"}}))
	if got := postgresFDWPID(t, p, c); got == pid {
		t.Fatal("Restore resurrected physical PID")
	}
	// Matching fresh Driver must see current source data through its own adapter.
	d := stuntgres.NewDriver()
	destination := &fixture.ExternalSQLProvider{}
	if err := postgresFDWConfigure(d, destination, fixture.ExternalSQLFixtureVersion, true); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("postgres-fdw-live-destination/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	db, recorder := fixture.RecordedNativePool(t, connector)
	defer raw.Close()
	defer db.Close()
	if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	if destination.OpenCalls() != 0 {
		t.Fatal("fresh Restore eagerly connected")
	}
	fixture.RunStep(t, p.Ctx, db, recorder, externalSQLProviderRows(false, "SELECT id,label,flag,bytes FROM pf_table WHERE id=1", [][]any{{int64(1), "after-dump", true, "\x00\xff"}}))
	if len(destination.Snapshot()) == 0 {
		t.Fatal("fresh Driver borrowed source connection")
	}
	if err := stuntgres.Reset(p.Ctx, raw); err != nil {
		t.Fatal(err)
	}
	for _, wire := range destination.Snapshot() {
		postgresFDWObserveGone(t, p, wire.Info().BackendPID)
	}
	postgresFDWPendingControls(t, p, provider, dump.String())
}
