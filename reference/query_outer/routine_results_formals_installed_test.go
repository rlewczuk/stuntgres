package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsFormalInstalled(t *testing.T) {
	for _, tc := range []struct {
		extension, name, identity, all, modes, names string
		inputs                                       int64
	}{
		{"pgrowlocks", "pgrowlocks", "25", "{25,27,28,16,1011,1009,1007}", "{i,o,o,o,o,o,o}", "{relname,locked_row,locker,multi,xids,modes,pids}", 1},
		{"amcheck", "verify_heapam", "2205 16 16 25 20 20", "{2205,16,16,25,20,20,20,23,23,25}", "{i,i,i,i,i,i,o,o,o,o}", "{relation,on_error_stop,check_toast,skip,startblock,endblock,blkno,offnum,attnum,msg}", 6},
	} {
		t.Run(tc.extension, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "rr_formal_moved")
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE EXTENSION "+tc.extension+" SCHEMA public")
				signature := map[string]string{"pgrowlocks": "(text)", "verify_heapam": "(regclass,boolean,boolean,text,bigint,bigint)"}[tc.name]
				schema := "public"
				var originalOID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname=$1::name AND pronamespace='public'::regnamespace", tc.name).Scan(&originalOID); err != nil || originalOID <= 0 {
					t.Fatal("installed member identity", originalOID, err)
				}
				check := func(c *sql.Conn, b fixture.Backend) {
					if _, err := c.ExecContext(p.Ctx, "SET search_path=rr_formal_moved,public,pg_catalog"); err != nil {
						t.Fatal(err)
					}
					q := "SELECT pronargs::integer AS inputs,proargtypes::text AS identity,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,provariadic::bigint AS variadic,proretset AS sets FROM pg_proc WHERE proname=$1::name"
					resultsFormalBoth(t, p, b, c, q, []string{"inputs", "identity", "alltypes", "modes", "names", "variadic", "sets"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL"}, [][]any{{tc.inputs, tc.identity, tc.all, tc.modes, tc.names, int64(0), true}}, tc.name)
					resultsFormalBoth(t, p, b, c, "SELECT oid=$1::regprocedure AS identity FROM pg_proc WHERE proname=$2::name", []string{"identity"}, []string{"BOOL"}, [][]any{{true}}, tc.name+signature, tc.name)
					// Moving a member copies its descriptor, not its installation.
					// Membership, owner and the extension's original namespace remain.
					q = "SELECT p.pronamespace=$1::regnamespace AS namespace,p.proowner=e.extowner AS owner,e.extnamespace='public'::regnamespace AS installation FROM (SELECT oid,pronamespace,proowner FROM pg_proc WHERE proname=$2::name) p JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname=$3::name"
					resultsFormalBoth(t, p, b, c, q, []string{"namespace", "owner", "installation"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, schema, tc.name, tc.extension)
				}
				check(c, b)
				resultsExec(t, p, b, c, "CREATE SCHEMA rr_formal_moved", "ALTER FUNCTION public."+tc.name+signature+" SET SCHEMA rr_formal_moved")
				schema = "rr_formal_moved"
				resultsFormalBoth(t, p, b, c, "SELECT oid::bigint AS identity FROM pg_proc WHERE proname=$1::name AND pronamespace='rr_formal_moved'::regnamespace", []string{"identity"}, []string{"INT8"}, [][]any{{originalOID}}, tc.name)
				check(c, b)
				// Existing ALTER FUNCTION descriptor-copy and native replay paths
				// preserve the declaration independently of the installation label.
				resultsFormalReplay(t, p, b, c, check)
			})
		})
	}
}

func resultsFormalReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL control equivalent.
	var emitted []byte
	for range 2 {
		emitted = resultsNativeReplayRound(t, p, b, c, check)
	}
	if p.Postgres != nil {
		func() {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			clean := func() {
				// The installation remains in public even when its member moved.
				if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS rr_formal_moved CASCADE; CREATE SCHEMA public; SET search_path=public,pg_catalog"); err != nil {
					t.Fatal(err)
				}
			}
			clean()
			if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("unchanged formal-owner dump replay", err)
			}
			if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true"); err != nil {
				t.Fatal(err)
			}
			check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			clean()
		}()
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/formal-owner")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	recorded, rec := fixture.RecordedNativePool(t, connector)
	// Occupy an independent owner's generation before replacing its catalog.
	fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TYPE rr_formal_collision AS (v integer)", 0))
	if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
		t.Fatal(err)
	}
	clone, err := recorded.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	check(clone, fixture.Backend{Recorder: rec})
}
