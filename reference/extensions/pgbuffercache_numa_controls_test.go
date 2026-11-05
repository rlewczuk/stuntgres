package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMAControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_numa_control_owner", "bc_numa_control_reader", "bc_numa_control_deny"})
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_numa_control_owner", "CREATE ROLE bc_numa_control_reader", "CREATE ROLE bc_numa_control_deny",
			"CREATE SCHEMA bc_moved",
			"GRANT USAGE ON SCHEMA bc_part,bc_moved TO bc_numa_control_owner,bc_numa_control_reader,bc_numa_control_deny",
			"CREATE TABLE bc_part.numa_control_heap(k integer)",
			"CREATE VIEW bc_part.numa_control_wrapper AS SELECT * FROM bc_part.pg_buffercache_numa")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.numa_control_heap VALUES(1)", 1))
		capacity := bcCapacity(t, p, c)
		original := bcNUMACatalogLinks(t, p, b, c, "bc_part.pg_buffercache_numa_pages()", "bc_part.pg_buffercache_numa")
		wrapper := "SELECT count(DISTINCT bufferid) AS n FROM bc_part.numa_control_wrapper"
		for _, q := range []string{
			"ALTER FUNCTION bc_part.pg_buffercache_numa_pages() RENAME TO numa_pages_kept",
			"ALTER FUNCTION bc_part.numa_pages_kept() SET SCHEMA bc_moved",
			"ALTER VIEW bc_part.pg_buffercache_numa RENAME TO numa_kept",
			"ALTER VIEW bc_part.numa_kept SET SCHEMA bc_moved",
			"ALTER EXTENSION pg_buffercache DROP FUNCTION bc_moved.numa_pages_kept()",
			"ALTER EXTENSION pg_buffercache DROP VIEW bc_moved.numa_kept",
		} {
			bcDDL(t, p, b, c, q)
			bcNUMABoth(t, p, b, c, bcNUMACount(wrapper, capacity))
		}
		bcNUMAMembership(t, p, b, c, original, 0)
		for _, tc := range []struct { class, object int64 }{{1255, original[0]}, {1259, original[1]}} {
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_init_privs WHERE classoid=$1::oid AND objoid=$2::oid AND objsubid=0 AND privtype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, tc.class, tc.object))
		}
		if got := bcNUMACatalogLinks(t, p, b, c, "bc_moved.numa_pages_kept()", "bc_moved.numa_kept"); !reflect.DeepEqual(got, original) {
			t.Fatal("move/rename/detach changed genuine identities", original, got)
		}
		bcDDL(t, p, b, c,
			"ALTER EXTENSION pg_buffercache ADD FUNCTION bc_moved.numa_pages_kept()",
			"ALTER EXTENSION pg_buffercache ADD VIEW bc_moved.numa_kept")
		bcNUMAMembership(t, p, b, c, original, 2)
		// ADD takes a new snapshot of the unchanged installation ACL. Later
		// ownership/grants, not arbitrary detach/re-add, distinguish history.
		for _, q := range []string{
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.oid='bc_moved.numa_pages_kept()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs=p.proacl",
			"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND c.oid='bc_moved.numa_kept'::regclass AND i.privtype='e' AND i.objsubid=0 AND i.initprivs=c.relacl",
		} {
			bcBoth(t, p, b, c, bcNUMACount(q, 1))
		}
		bcDDL(t, p, b, c,
			"ALTER FUNCTION bc_moved.numa_pages_kept() OWNER TO bc_numa_control_owner",
			"ALTER VIEW bc_moved.numa_kept OWNER TO bc_numa_control_owner",
			"ALTER TABLE bc_part.numa_control_heap OWNER TO bc_numa_control_owner",
			"GRANT EXECUTE ON FUNCTION bc_moved.numa_pages_kept() TO bc_numa_control_reader",
			"GRANT SELECT ON bc_moved.numa_kept TO bc_numa_control_reader",
			"CREATE FUNCTION bc_part.pg_buffercache_numa_pages() RETURNS integer LANGUAGE SQL AS 'SELECT 77'",
			"CREATE VIEW bc_part.pg_buffercache_numa AS SELECT 88 AS sentinel")
		movedFunction := bcFrom("bc_moved.numa_pages_kept", bcNUMANames, bcNUMASQLTypes)
		viewQ := "SELECT count(DISTINCT bufferid) AS n FROM bc_moved.numa_kept"
		functionQ := "SELECT count(DISTINCT bufferid) AS n FROM " + movedFunction
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post := func(target *sql.Conn, backend fixture.Backend) {
			t.Helper()
			ids := bcNUMACatalogLinks(t, p, backend, target, "bc_moved.numa_pages_kept()", "bc_moved.numa_kept")
			bcNUMAMembership(t, p, backend, target, ids, 2)
			bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(proowner) AS owner,prosrc FROM pg_proc WHERE oid='bc_moved.numa_pages_kept()'::regprocedure", []string{"owner", "prosrc"}, []string{"NAME", "TEXT"}, [][]any{{"bc_numa_control_owner", "pg_buffercache_numa_pages"}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(c.relowner) AS owner,pg_get_userbyid(t.typowner) AS row_owner,pg_get_userbyid(a.typowner) AS array_owner FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='bc_moved.numa_kept'::regclass", []string{"owner", "row_owner", "array_owner"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"bc_numa_control_owner", "bc_numa_control_owner", "bc_numa_control_owner"}}))
			for _, tc := range []struct { class int; object, ownerACL, monitor string }{
				{1255, "'bc_moved.numa_pages_kept()'::regprocedure", "=X/", "pg_monitor=X/"},
				{1259, "'bc_moved.numa_kept'::regclass", "=arwdDxtm/", "pg_monitor=r/"},
			} {
				q := "SELECT initprivs=ARRAY[(current_user::text||$1::text||current_user::text)::aclitem,($2::text||current_user::text)::aclitem] AS initial FROM pg_init_privs WHERE classoid=$3::oid AND objoid=" + tc.object + " AND objsubid=0 AND privtype='e'"
				bcBoth(t, p, backend, target, fixture.Select(q, []string{"initial"}, []string{"BOOL"}, [][]any{{true}}, tc.ownerACL, tc.monitor, tc.class))
			}
			for _, q := range []string{
				"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.oid='bc_moved.numa_pages_kept()'::regprocedure AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>p.proacl",
				"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND c.oid='bc_moved.numa_kept'::regclass AND i.privtype='e' AND i.objsubid=0 AND i.initprivs<>c.relacl",
			} {
				bcBoth(t, p, backend, target, bcNUMACount(q, 1))
			}
			bcBoth(t, p, backend, target, fixture.Select("SELECT bc_part.pg_buffercache_numa_pages() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT sentinel FROM bc_part.pg_buffercache_numa", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(88)}}))
			for _, q := range []string{wrapper, viewQ, functionQ} {
				bcNUMABoth(t, p, backend, target, bcNUMACount(q, capacity))
			}
			bcDDL(t, p, backend, target, "SET SESSION AUTHORIZATION bc_numa_control_deny")
			bcFailure(t, p, backend, target, bcError(viewQ, "42501", "permission denied for view numa_kept"), "execute")
			bcFailure(t, p, backend, target, bcError(functionQ, "42501", "permission denied for function numa_pages_kept"), "execute")
			bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION bc_numa_control_reader")
			for _, q := range []string{viewQ, functionQ} {
				bcNUMABoth(t, p, backend, target, bcNUMACount(q, capacity))
			}
			bcFailure(t, p, backend, target, bcError("SELECT k FROM bc_part.numa_control_heap", "42501", "permission denied for table numa_control_heap"), "execute")
			bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION")
		}
		post(c, b)
		if got := bcNUMACatalogLinks(t, p, b, c, "bc_moved.numa_pages_kept()", "bc_moved.numa_kept"); !reflect.DeepEqual(got, original) {
			t.Fatal("owner/ACL/shadow changed provider identity", original, got)
		}
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_control_heap")
		bcUsage(t, p, b, c, "bc_part.numa_control_heap", 1)
		if b.PG {
			return
		}
		t.Run("native_control_roundtrip", func(t *testing.T) {
			databases = append(databases, "bc_numa_control_other")
			bcDDL(t, p, b, c, "CREATE DATABASE bc_numa_control_other")
			otherPool, _, otherRecorder := fixture.DatabaseScenarioPool(t, p, false, "bc_numa_control_other", "")
			other, err := otherPool.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer other.Close()
			ob := b
			ob.DB, ob.Recorder = otherPool, otherRecorder
			bcDDL(t, p, ob, other, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part", "CREATE TABLE bc_part.other_heap(k integer)")
			bcRun(t, p, ob, other, fixture.Exec("INSERT INTO bc_part.other_heap VALUES(1)", 1))
			bcReadHeap(t, p, ob, other, "SELECT k FROM bc_part.other_heap")
			for range 3 { bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_control_heap") }
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 4)
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 4)
			st, err := c.PrepareContext(p.Ctx, viewQ)
			if err != nil { t.Fatal(err) }
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: viewQ}
			borrower, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer borrower.Close()
			old, err := borrower.QueryContext(p.Ctx, "SELECT * FROM bc_moved.numa_kept ORDER BY bufferid")
			if err != nil { t.Fatal(err) }
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			retained := bcRows(t, old, false, true, bcNUMANames, bcNUMATypes)
			bcNUMATopology(t, retained, false, capacity)
			if !reflect.DeepEqual(retained[0], []any{int64(1), int64(0), int64(0)}) || !reflect.DeepEqual(retained[len(retained)-1], []any{capacity, capacity-1, int64(0)}) {
				t.Fatal("retained NUMA rows changed across Reset", retained[0], retained[len(retained)-1])
			}
			for i, col := range metadata {
				if col.Name() != bcNUMANames[i] || col.DatabaseTypeName() != bcNUMATypes[i] { t.Fatal("retained metadata mutated", i, col) }
			}
			if err := borrower.Close(); err != nil { t.Fatal(err) }
			bcUsage(t, p, ob, other, "bc_part.other_heap", 1)
			for _, q := range []string{
				"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
				"SELECT count(*) AS n FROM pg_class WHERE relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='bc_part') AND relname='numa_control_heap'",
			} { bcBoth(t, p, b, c, bcNUMACount(q, 0)) }
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 0)
			bcNUMARead(t, p, b, c, "SELECT * FROM bc_moved.numa_kept", true, capacity)
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 0)
			post(c, b)
			bcNUMADemand(t, p, b, held, bcNUMACount(viewQ, capacity))
			bcUsage(t, p, ob, other, "bc_part.other_heap", 1)
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_control_heap")
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 1)
			before := fixture.ExtensionDump(t, p.Ctx, p.Native)
			for _, bad := range []string{dump+"\nINVALID SQL;", dump+"\nCREATE EXTENSION bc_missing_provider;"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil { t.Fatal("malformed Restore accepted") }
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("failed Restore published SQL/ACL/data prefix") }
				bcUsage(t, p, b, c, "bc_part.numa_control_heap", 1)
				post(c, b)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) { t.Fatal("canceled Restore", err) }
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before { t.Fatal("canceled Restore published SQL/ACL/data") }
			post(c, b)
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 1)

			connector, err := stuntgres.NewDriver().OpenConnector("numa-control-independent-destination")
			if err != nil { t.Fatal(err) }
			destination := sql.OpenDB(connector)
			defer destination.Close()
			pool, rec := fixture.RecordedNativePool(t, connector)
			dest, err := pool.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer dest.Close()
			defer dest.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			db := b
			db.DB, db.Recorder = pool, rec
			bcDDL(t, p, db, dest, "CREATE TABLE destination_sentinel(k integer)")
			bcRun(t, p, db, dest, fixture.Exec("INSERT INTO destination_sentinel VALUES(1)", 1))
			beforeDest := fixture.ExtensionDump(t, p.Ctx, destination)
			if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err == nil { t.Fatal("Restore invented named principals") }
			if fixture.ExtensionDump(t, p.Ctx, destination) != beforeDest { t.Fatal("missing-principal Restore was not atomic") }
			bcReadHeap(t, p, db, dest, "SELECT k FROM destination_sentinel")
			bcDDL(t, p, db, dest, "CREATE ROLE bc_numa_control_owner", "CREATE ROLE bc_numa_control_reader", "CREATE ROLE bc_numa_control_deny")
			if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err != nil { t.Fatal(err) }
			bcUsage(t, p, db, dest, "bc_part.numa_control_heap", 0)
			bcNUMARead(t, p, db, dest, "SELECT * FROM bc_moved.numa_kept", true, bcCapacity(t, p, dest))
			post(dest, db)
			bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.numa_control_heap")
			bcUsage(t, p, db, dest, "bc_part.numa_control_heap", 1)
			for range 2 { bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.numa_control_heap") }
			bcUsage(t, p, db, dest, "bc_part.numa_control_heap", 3)
			bcUsage(t, p, b, c, "bc_part.numa_control_heap", 1)
			bcUsage(t, p, ob, other, "bc_part.other_heap", 1)
		})
	})
}
