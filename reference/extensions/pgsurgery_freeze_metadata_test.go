package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeMetadata(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Select freeze independently of the genuine kill cohort member.
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='fs_part' AND p.proname='heap_force_freeze'"
		freezeBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT"},
			[][]any{{"heap_force_freeze", int64(2), "2205 1010", int64(2278), nil, nil, "{reloid,tids}", false, true, "v", "u", false, "heap_force_freeze", "$libdir/pg_surgery", "c"}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,n.nspname::text AS schema FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE extname='pg_surgery'", []string{"extversion", "extrelocatable", "schema"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.0", true, "fs_part"}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='pg_surgery'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"pg_surgery", "1.0", "1.0", "extension to perform surgery on a damaged relation"}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT installed,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_surgery' AND version='1.0'", []string{"installed", "superuser", "trusted", "relocatable"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, true}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT d.deptype::text AS dependency FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='fs_part.heap_force_freeze(regclass,tid[])'::regprocedure AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_surgery')", []string{"dependency"}, []string{"TEXT"}, [][]any{{"e"}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT i.privtype::text AS kind,i.initprivs::text=p.proacl::text AS matches FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid='pg_proc'::regclass AND i.objoid='fs_part.heap_force_freeze(regclass,tid[])'::regprocedure AND i.objsubid=0", []string{"kind", "matches"}, []string{"TEXT", "BOOL"}, [][]any{{"e", true}}))
		for _, q := range []string{"SELECT fs_part.heap_force_freeze(reloid=>'fs_part.heap'::regclass,tids=>'{}'::tid[]) AS repaired", "SELECT fs_part.heap_force_freeze(tids=>'{}'::tid[],reloid=>'fs_part.heap'::regclass) AS repaired"} {
			freezeBoth(t, p, b, c, freezeVoid(q))
		}
		freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_surgery SET SCHEMA public", 0))
		freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		freezeBoth(t, p, b, c, freezeVoid(freezeSQL, "fs_part.heap", "{}"))
		freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
		freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pg_surgery SCHEMA fs_part", 0))
		freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		freezeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_surgery'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='fs_part' AND p.proname='heap_force_freeze'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferencePgsurgeryFreezeSystemColumns(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeBoth(t, p, b, c, fixture.Select("SELECT h.ctid,h.xmin,h.xmax FROM fs_part.heap h WHERE false", []string{"ctid", "xmin", "xmax"}, []string{"TID", "XID", "XID"}, nil))
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		freezeBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.heap", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}}))
		q := "SELECT h.ctid,h.xmin,h.xmax FROM fs_part.heap h WHERE h.ctid=$1::tid"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		checkHeld := func(h freezeHeader) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"ctid", "xmin", "xmax"}, []string{"TID", "XID", "XID"}, [][]any{{h.tid, h.xmin, h.xmax}}, h.tid))
		}
		checkHeld(a)
		freezeRun(t, p, b, c, fixture.Exec("CREATE VIEW fs_part.headers AS SELECT k,ctid,xmin,xmax FROM fs_part.heap", 0))
		freezeApply(t, p, b, c, a.tid)
		a = freezeRepaired(a)
		checkHeld(a)
		freezeBoth(t, p, b, c, fixture.Select("SELECT k,ctid,xmin,xmax FROM fs_part.headers", []string{"k", "ctid", "xmin", "xmax"}, []string{"INT4", "TID", "XID", "XID"}, [][]any{{int64(1), a.tid, "2", "0"}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT h.ctid,(SELECT h.xmin) AS born FROM fs_part.heap h WHERE h.xmax='0'::xid", []string{"ctid", "born"}, []string{"TID", "XID"}, [][]any{{a.tid, "2"}}))
		missing := freezeError("SELECT ctid FROM fs_part.heap a CROSS JOIN fs_part.heap b", "42703", "column \"ctid\" does not exist")
		missing.Diagnostic.Position = "8"
		missing.Diagnostic.Detail = "There are columns named \"ctid\", but they are in tables that cannot be referenced from this part of the query."
		missing.Diagnostic.Hint = "Try using a table-qualified name."
		freezeBoth(t, p, b, c, missing)
		u := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
		if u.tid == a.tid || u.xmin == "2" {
			t.Fatal("UPDATE reused frozen physical header", u)
		}
		d := freezeOne(t, p, c, "DELETE FROM fs_part.heap WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", u.tid)
		if d.tid != u.tid || d.k != 1 || d.v != "B" || d.xmax == "0" {
			t.Fatal("DELETE RETURNING physical header", d)
		}
		for _, q := range []string{"CREATE TABLE fs_part.parent(k integer,v text)", "CREATE TABLE fs_part.child() INHERITS(fs_part.parent)"} {
			freezeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		child := freezeOne(t, p, c, "INSERT INTO fs_part.child VALUES(7,'child') RETURNING k,v,ctid,xmin,xmax")
		freezeBoth(t, p, b, c, fixture.Select("SELECT ctid,xmin,xmax,tableoid='fs_part.child'::regclass AS physical FROM fs_part.parent", []string{"ctid", "xmin", "xmax", "physical"}, []string{"TID", "XID", "XID", "BOOL"}, [][]any{{child.tid, child.xmin, child.xmax, true}}))
		freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.zero()", 0))
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.zero DEFAULT VALUES", 1))
		freezeBoth(t, p, b, c, fixture.Select("SELECT ctid IS NOT NULL AS tid,xmin IS NOT NULL AS born,xmax='0'::xid AS clean FROM fs_part.zero", []string{"tid", "born", "clean"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		freezeBoth(t, p, b, c, fixture.Select("SELECT s.ctid,s.xmin,s.xmax FROM (SELECT 11 AS ctid,12 AS xmin,13 AS xmax) s", []string{"ctid", "xmin", "xmax"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(12), int64(13)}}))
		for _, column := range []string{"ctid", "xmin", "xmax"} {
			query := "UPDATE fs_part.heap SET " + column + "=NULL"
			s := freezeError(query, "0A000", "cannot assign to system column \""+column+"\"")
			s.SelectRows = false
			s.Diagnostic.Position = fixture.Position(strings.Index(query, column) + 1)
			freezeBoth(t, p, b, c, s)
		}
	})
}

func TestReferencePgsurgeryFreezeFormalSlots(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				freezeRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE fs_part.eval_order", 0))
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'formal-slot') RETURNING k,v,ctid,xmin,xmax")
				q := "SELECT fs_part.heap_force_freeze(tids=>ARRAY[CASE WHEN nextval('fs_part.eval_order')=2 THEN $1::tid ELSE '(0,0)'::tid END],reloid=>CASE WHEN nextval('fs_part.eval_order')=1 THEN 'fs_part.heap'::regclass ELSE 0::regclass END) AS repaired"
				s := freezeVoid(q, a.tid)
				s.Prepared = prepared
				freezeRun(t, p, b, c, s)
				freezeExpect(t, p, b, c, freezeRepaired(a))
				freezeBoth(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM fs_part.eval_order", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
			})
		})
	}
}
