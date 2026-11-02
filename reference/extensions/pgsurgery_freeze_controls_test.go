package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_control_owner"})
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		horizon := freezeHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'kept') RETURNING k,v,ctid,xmin,xmax")
		d := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'repair') RETURNING k,v,ctid,xmin,xmax")
		freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=2", 1))
		freezeApply(t, p, b, c, d.tid)
		freezeExpect(t, p, b, c, a, freezeRepaired(d))
		predecessor := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(3,'predecessor') RETURNING k,v,ctid,xmin,xmax")
		successor := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='successor' WHERE k=3 RETURNING k,v,ctid,xmin,xmax")
		if predecessor.tid == successor.tid {
			t.Fatal("control fixture lost distinct physical versions")
		}
		freezeApply(t, p, b, c, predecessor.tid)
		freezeExpect(t, p, b, c, a, freezeRepaired(d), freezeRepaired(predecessor), successor)
		freezeRun(t, p, b, horizon, fixture.Exec("ROLLBACK", 0))
		for _, q := range []string{
			"CREATE ROLE fs_control_owner", "CREATE SCHEMA fs_moved",
			"CREATE VIEW fs_part.headers AS SELECT k,ctid,xmin,xmax FROM fs_part.heap",
			"ALTER TABLE fs_part.heap OWNER TO fs_control_owner",
			"GRANT USAGE ON SCHEMA fs_part,fs_moved TO fs_control_owner",
			"GRANT EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) TO fs_control_owner",
			"ALTER FUNCTION fs_part.heap_force_freeze(regclass,tid[]) RENAME TO repair",
			"ALTER FUNCTION fs_part.repair(regclass,tid[]) SET SCHEMA fs_moved",
			"ALTER EXTENSION pg_surgery DROP FUNCTION fs_moved.repair(regclass,tid[])",
		} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k,v", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "repair"}, {int64(3), "predecessor"}, {int64(3), "successor"}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT k,ctid IS NOT NULL AS tid,xmin IS NOT NULL AS born,xmax='0'::xid AS clean FROM fs_part.headers ORDER BY k", []string{"k", "tid", "born", "clean"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true}, {int64(2), true, true, true}, {int64(3), true, true, true}, {int64(3), true, true, true}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT r.rolname::text AS owner FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='fs_part.heap'::regclass", []string{"owner"}, []string{"TEXT"}, [][]any{{"fs_control_owner"}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='fs_moved.repair(regclass,tid[])'::regprocedure AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		post(c, b.Recorder)
		if b.PG {
			return
		} // Public native controls have no server analogue.
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		old, err := c.QueryContext(p.Ctx, "SELECT k,v FROM fs_part.heap ORDER BY k,v")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, want := range []struct {
			k int64
			v string
		}{{1, "kept"}, {2, "repair"}, {3, "predecessor"}, {3, "successor"}} {
			if !old.Next() {
				t.Fatal("retained Rows lost row", old.Err())
			}
			var k int64
			var v string
			if err := old.Scan(&k, &v); err != nil || k != want.k || v != want.v {
				t.Fatal("retained row", k, v, err)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore changed destination")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled restore changed destination")
		}
		other, dest, rec := pgcryptoControlDestination(t, p, "fs_freeze_destination")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE ROLE fs_control_owner", 0))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(dest, rec)
		restored := freezeOne(t, p, dest, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=2")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("DELETE FROM fs_part.heap WHERE k=2", 1))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("SET SESSION AUTHORIZATION fs_control_owner", 0))
		defer dest.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, dest, rec, freezeVoid("SELECT fs_moved.repair($1::regclass,$2::tid[]) AS repaired", "fs_part.heap", freezeTIDs(restored.tid)))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		post(dest, rec)
		post(c, b.Recorder)
	})
}

func TestReferencePgsurgeryFreezeFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_smoke_owner"})
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1,'kept'),(2,'repair')", 2))
		h := freezeCapture(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap ORDER BY k")
		if len(h) != 2 || h[0].k != 1 || h[0].v != "kept" || h[1].k != 2 || h[1].v != "repair" {
			t.Fatal("independent smoke payload", h)
		}
		freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=2", 1))
		st, err := c.PrepareContext(p.Ctx, freezeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: freezeSQL}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs(h[1].tid)))
		h[1] = freezeRepaired(h[1])
		freezeExpect(t, p, b, c, h...)
		freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		freezeApply(t, p, b, c, h[1].tid)
		freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		freezeExpect(t, p, b, c, h...)
		freezeBoth(t, p, b, c, fixture.Select(freezeSQL, []string{"repaired"}, []string{"VOID"}, [][]any{{nil}}, nil, nil))
		freezeBoth(t, p, b, c, freezeError(freezeSQL, "22004", "array must not contain nulls", "fs_part.heap", "{NULL}"))
		freezeExpect(t, p, b, c, h...)
		for _, q := range []string{"CREATE ROLE fs_smoke_owner", "ALTER TABLE fs_part.heap OWNER TO fs_smoke_owner", "GRANT USAGE ON SCHEMA fs_part TO fs_smoke_owner"} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_smoke_owner", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeError(freezeSQL, "42501", "permission denied for function heap_force_freeze", "fs_part.heap", freezeTIDs(h[1].tid)))
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION fs_part.heap_force_freeze(regclass,tid[]) TO fs_smoke_owner", 0))
		freezeRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_smoke_owner", 0))
		notices = nil
		fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs(h[1].tid)))
		if len(notices) != 0 {
			t.Fatalf("unexpected surgery warning: %#v", notices)
		}
		freezeRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		other, dest, rec := pgcryptoControlDestination(t, p, "fs_smoke_destination")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE ROLE fs_smoke_owner", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("CREATE TABLE destination_replaced(k integer)", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO destination_replaced VALUES(99)", 1))
		if err := stuntgres.Reset(p.Ctx, other); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}, {int64(2), "repair"}}))
		d := freezeOne(t, p, dest, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=2")
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("DELETE FROM fs_part.heap WHERE k=2", 1))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("SET SESSION AUTHORIZATION fs_smoke_owner", 0))
		defer dest.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, dest, rec, freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs(d.tid)))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k,v,xmin,xmax FROM fs_part.heap WHERE k=2", []string{"k", "v", "xmin", "xmax"}, []string{"INT4", "TEXT", "XID", "XID"}, [][]any{{int64(2), "repair", "2", "0"}}))
	})
}
