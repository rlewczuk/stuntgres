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

func TestReferencePgBuffercacheControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_control_owner", "bc_control_reader", "bc_control_deny"})
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_control_owner", "CREATE ROLE bc_control_reader", "CREATE ROLE bc_control_deny",
			"GRANT USAGE ON SCHEMA bc_part TO bc_control_owner,bc_control_reader,bc_control_deny",
			"CREATE TABLE bc_part.control_heap(k integer)",
			"ALTER TABLE bc_part.control_heap OWNER TO bc_control_owner",
			"ALTER FUNCTION bc_part.pg_buffercache_pages() OWNER TO bc_control_owner",
			"ALTER VIEW bc_part.pg_buffercache OWNER TO bc_control_owner",
			"GRANT EXECUTE ON FUNCTION bc_part.pg_buffercache_pages() TO bc_control_reader",
			"GRANT SELECT ON bc_part.pg_buffercache TO bc_control_reader",
			"CREATE VIEW bc_part.control_wrapper AS SELECT * FROM bc_part.pg_buffercache")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.control_heap VALUES(1)", 1))
		capacity := bcCapacity(t, p, c)
		post := func(target *sql.Conn, backend fixture.Backend) {
			t.Helper()
			bcBoth(t, p, backend, target, fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='pg_buffercache'", []string{"extversion", "extrelocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"1.6", true}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='bc_part.pg_buffercache_pages()'::regprocedure", []string{"owner"}, []string{"NAME"}, [][]any{{"bc_control_owner"}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='bc_part.pg_buffercache'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"bc_control_owner"}}))
			for _, q := range []string{
				"SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid='bc_part.pg_buffercache_pages()'::regprocedure AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'",
				"SELECT count(*) AS n FROM pg_depend WHERE classid=1259 AND objid='bc_part.pg_buffercache'::regclass AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'",
				"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND i.objoid='bc_part.pg_buffercache_pages()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>p.proacl",
				"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND i.objoid='bc_part.pg_buffercache'::regclass AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>c.relacl",
			} {
				bcBoth(t, p, backend, target, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
			for _, tc := range []struct {
				class       int
				object, acl string
			}{
				{1255, "'bc_part.pg_buffercache_pages()'::regprocedure", "=X/"},
				{1259, "'bc_part.pg_buffercache'::regclass", "=arwdDxtm/"},
			} {
				monitor := "pg_monitor=X/"
				if tc.class == 1259 {
					monitor = "pg_monitor=r/"
				}
				q := "SELECT initprivs=ARRAY[(current_user::text||$1::text||current_user::text)::aclitem,($2::text||current_user::text)::aclitem] AS initial FROM pg_init_privs WHERE classoid=$3::oid AND objoid=" + tc.object + " AND objsubid=0 AND privtype='e'"
				bcBoth(t, p, backend, target, fixture.Select(q, []string{"initial"}, []string{"BOOL"}, [][]any{{true}}, tc.acl, monitor, tc.class))
			}
			bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM bc_part.control_wrapper", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
			bcDDL(t, p, backend, target, "SET SESSION AUTHORIZATION bc_control_deny")
			bcFailure(t, p, backend, target, bcError("SELECT * FROM bc_part.pg_buffercache", "42501", "permission denied for view pg_buffercache"), "execute")
			bcFailure(t, p, backend, target, bcError("SELECT * FROM "+bcFunction(), "42501", "permission denied for function pg_buffercache_pages"), "execute")
			bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION bc_control_reader")
			bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM bc_part.pg_buffercache", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
			bcFailure(t, p, backend, target, bcError("SELECT k FROM bc_part.control_heap", "42501", "permission denied for table control_heap"), "execute")
			bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION")
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post(c, b)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.control_heap")
		bcUsage(t, p, b, c, "bc_part.control_heap", 1)
		if b.PG {
			return
		}
		databases = append(databases, "bc_control_other")
		bcDDL(t, p, b, c, "CREATE DATABASE bc_control_other")
		otherPool, _, otherRecorder := fixture.DatabaseScenarioPool(t, p, false, "bc_control_other", "")
		other, err := otherPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		ob := b
		ob.DB, ob.Recorder = otherPool, otherRecorder
		bcDDL(t, p, ob, other, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part", "CREATE TABLE bc_part.control_other_heap(k integer)")
		bcRun(t, p, ob, other, fixture.Exec("INSERT INTO bc_part.control_other_heap VALUES(1)", 1))
		bcReadHeap(t, p, ob, other, "SELECT k FROM bc_part.control_other_heap")
		bcUsage(t, p, ob, other, "bc_part.control_other_heap", 1)

		for range 4 {
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.control_heap")
		}
		bcUsage(t, p, b, c, "bc_part.control_heap", 5)
		oldNode, oldSpace, oldDatabase := bcStorage(t, p, c, "bc_part.control_heap")
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		bcUsage(t, p, b, c, "bc_part.control_heap", 5) // Dump is not a table read.
		q := "SELECT count(*) AS n FROM " + bcFunction()
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		borrower, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", oldNode, oldSpace, oldDatabase)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		bcUsage(t, p, ob, other, "bc_part.control_other_heap", 1)
		retained := bcRows(t, old, false, true, bcNames, bcTypes)
		if len(retained) != 1 || retained[0][1] != oldNode || retained[0][7] != int64(5) || retained[0][8] != int64(0) {
			t.Fatal("old detached cache snapshot changed across Reset", retained)
		}
		if err := borrower.Close(); err != nil {
			t.Fatal(err)
		}
		for i, col := range metadata {
			if col.Name() != bcNames[i] || col.DatabaseTypeName() != bcTypes[i] {
				t.Fatal("retained metadata changed", i, col)
			}
		}
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='bc_part') AND relname='control_heap'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bcUsage(t, p, ob, other, "bc_part.control_other_heap", 1)
		bcUsage(t, p, b, c, "bc_part.control_heap", 0)
		post(c, b)
		bcRun(t, p, b, held, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.control_heap")
		bcUsage(t, p, b, c, "bc_part.control_heap", 1)
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		for _, bad := range []string{dump + "\nINVALID SQL;", dump + "\nCREATE EXTENSION bc_missing_provider;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("malformed Restore accepted")
			}
			if got := fixture.ExtensionDump(t, p.Ctx, p.Native); got != before {
				t.Fatal("failed Restore published a prefix")
			}
			bcUsage(t, p, b, c, "bc_part.control_heap", 1)
			post(c, b)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore boundary", err)
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled Restore changed catalog")
		}
		bcUsage(t, p, b, c, "bc_part.control_heap", 1)
		post(c, b)

		connector, err := stuntgres.NewDriver().OpenConnector("buffercache-control-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, rec := fixture.RecordedNativePool(t, connector)
		dest, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		defer dest.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		db := b
		db.DB, db.Recorder = pool, rec
		bcDDL(t, p, db, dest, "CREATE TABLE destination_sentinel(k integer)")
		beforeDest := fixture.ExtensionDump(t, p.Ctx, destination)
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err == nil {
			t.Fatal("Restore invented named owners/ACL roles")
		}
		if fixture.ExtensionDump(t, p.Ctx, destination) != beforeDest {
			t.Fatal("missing-owner Restore changed destination")
		}
		bcDDL(t, p, db, dest, "CREATE ROLE bc_control_owner", "CREATE ROLE bc_control_reader", "CREATE ROLE bc_control_deny")
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		// Destination numeric OIDs are allocator-owned. Prove resident mapped
		// relationships rather than requiring source allocator numbers.
		bcUsage(t, p, db, dest, "bc_part.control_heap", 0)
		post(dest, db)
		bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.control_heap")
		bcUsage(t, p, db, dest, "bc_part.control_heap", 1)
		for range 2 {
			bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.control_heap")
		}
		bcUsage(t, p, db, dest, "bc_part.control_heap", 3)
		bcUsage(t, p, b, c, "bc_part.control_heap", 1)
	})
}
