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

func TestReferencePgBuffercacheEvictionControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_evict_control_owner", "bc_evict_control_reader", "bc_evict_control_deny"})
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_evict_control_owner", "CREATE ROLE bc_evict_control_reader", "CREATE ROLE bc_evict_control_deny",
			"GRANT USAGE ON SCHEMA bc_part TO bc_evict_control_owner,bc_evict_control_reader,bc_evict_control_deny")
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		bceHeap(t, p, b, c, "evict_control_heap")
		for _, signature := range []string{"pg_buffercache_evict(integer)", "pg_buffercache_evict_relation(regclass)", "pg_buffercache_evict_all()"} {
			bcDDL(t, p, b, c, "ALTER FUNCTION bc_part."+signature+" OWNER TO bc_evict_control_owner", "REVOKE EXECUTE ON FUNCTION bc_part."+signature+" FROM PUBLIC", "GRANT EXECUTE ON FUNCTION bc_part."+signature+" TO bc_evict_control_reader")
		}
		t.Run("record_column_is_not_a_storable_type", func(t *testing.T) {
			s := bcError("CREATE VIEW bc_part.control_evict_bad_record AS SELECT bc_part.pg_buffercache_evict_relation(NULL::regclass) AS result", "42P16", "column \"result\" has pseudo-type record")
			s.SelectRows = false
			bcFailure(t, p, b, c, s, "execute")
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='control_evict_bad_record'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
		bcDDL(t, p, b, c,
			"CREATE VIEW bc_part.control_evict_single AS SELECT * FROM bc_part.pg_buffercache_evict(NULL::integer)",
			"CREATE VIEW bc_part.control_evict_relation AS SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.evict_control_heap'::regclass)",
			"CREATE VIEW bc_part.control_evict_all AS SELECT * FROM bc_part.pg_buffercache_evict_all()",
			"CREATE VIEW bc_part.control_evict_scalar AS SELECT e.buffers_evicted AS result FROM bc_part.pg_buffercache_evict_relation(NULL::regclass) AS e",
			"CREATE VIEW bc_part.control_evict_scalar_field AS SELECT (bc_part.pg_buffercache_evict_relation(NULL::regclass)).buffers_evicted AS result",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT EXECUTE ON FUNCTIONS TO bc_evict_control_deny")
		post := func(backend fixture.Backend, target *sql.Conn) {
			t.Helper()
			for _, signature := range []string{"pg_buffercache_evict(integer)", "pg_buffercache_evict_relation(regclass)", "pg_buffercache_evict_all()"} {
				bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(proowner) AS owner,has_function_privilege('bc_evict_control_reader',oid,'EXECUTE') AS reader,has_function_privilege('bc_evict_control_deny',oid,'EXECUTE') AS denied FROM pg_proc WHERE oid=$1::regprocedure", []string{"owner", "reader", "denied"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"bc_evict_control_owner", true, false}}, "bc_part."+signature))
				bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::regprocedure AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, "bc_part."+signature))
			}
			for _, dependency := range []struct{ view, routine string }{
				{"control_evict_single", "pg_buffercache_evict(integer)"},
				{"control_evict_relation", "pg_buffercache_evict_relation(regclass)"},
				{"control_evict_all", "pg_buffercache_evict_all()"},
				{"control_evict_scalar", "pg_buffercache_evict_relation(regclass)"},
				{"control_evict_scalar_field", "pg_buffercache_evict_relation(regclass)"},
			} {
				q := "SELECT count(*) AS n FROM pg_rewrite r JOIN pg_depend d ON d.classid=2618 AND d.objid=r.oid WHERE r.ev_class=$1::regclass AND d.refclassid=1255 AND d.refobjid=$2::regprocedure AND d.deptype='n'"
				bcBoth(t, p, backend, target, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, "bc_part."+dependency.view, "bc_part."+dependency.routine))
			}
			bcBoth(t, p, backend, target, bceNullStep("SELECT * FROM bc_part.control_evict_single", true))
			for _, name := range []string{"control_evict_scalar", "control_evict_scalar_field"} {
				bcBoth(t, p, backend, target, fixture.Select("SELECT result FROM bc_part."+name, []string{"result"}, []string{"INT4"}, [][]any{{nil}}))
			}
			for _, name := range []string{"control_evict_relation", "control_evict_all"} {
				bcBoth(t, p, backend, target, fixture.Select("SELECT * FROM bc_part."+name+" WHERE false", bceCountNames, bceCountTypes, nil))
			}
			bcDDL(t, p, backend, target, "SET ROLE bc_evict_control_reader")
			bcFailure(t, p, backend, target, bceRuntimeDenial("SELECT * FROM bc_part.pg_buffercache_evict_all()", "pg_buffercache_evict_all"), "execute")
			bcDDL(t, p, backend, target, "RESET ROLE", "SET ROLE bc_evict_control_deny")
			bcFailure(t, p, backend, target, bcError("SELECT * FROM bc_part.pg_buffercache_evict_all()", "42501", "permission denied for function pg_buffercache_evict_all"), "execute")
			bcDDL(t, p, backend, target, "RESET ROLE")
		}
		wrapped := func(backend fixture.Backend, target *sql.Conn, name string, prepared bool) {
			t.Helper()
			query := "SELECT * FROM bc_part." + name
			var source fixture.Executor = target
			if prepared {
				st, err := target.PrepareContext(p.Ctx, query)
				fixture.State(t, err, "")
				defer st.Close()
				source = fixture.HeldStatement{Executor: target, Statement: st, SQL: query}
			}
			rows, err := source.QueryContext(p.Ctx, query)
			fixture.State(t, err, "")
			got := bcRows(t, rows, backend.PG, true, bceCountNames, bceCountTypes)
			if len(got) != 1 {
				t.Fatal("restored declared-OUT stored caller cardinality", got)
			}
			bceCounts(t, backend, got[0], bcCapacity(t, p, target), 1, 0)
			bceCold(t, p, backend, target, "evict_control_heap")
		}
		post(b, c)
		// Restore default configuration before Dump; the genuine installed and
		// current routine ACLs remain independent of this later default.
		bcDDL(t, p, b, c, "ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part REVOKE EXECUTE ON FUNCTIONS FROM bc_evict_control_deny")
		post(b, c)
		bceWarm(t, p, b, c, "evict_control_heap")
		if b.PG {
			wrapped(b, c, "control_evict_relation", true)
			return
		}
		databases = append(databases, "bc_evict_control_other")
		bcDDL(t, p, b, c, "CREATE DATABASE bc_evict_control_other")
		otherPool, _, otherRec := fixture.DatabaseScenarioPool(t, p, false, "bc_evict_control_other", "")
		other, err := otherPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		ob := b
		ob.DB, ob.Recorder = otherPool, otherRec
		bcDDL(t, p, ob, other, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		bceHeap(t, p, ob, other, "other_control_heap")
		peer := bceWarm(t, p, ob, other, "other_control_heap")
		for range 3 {
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.evict_control_heap")
		}
		bcUsage(t, p, b, c, "bc_part.evict_control_heap", 4)
		before := bcTarget(t, p, b, c, "bc_part.evict_control_heap")
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if after := bcTarget(t, p, b, c, "bc_part.evict_control_heap"); !reflect.DeepEqual(before, after) {
			t.Fatal("Dump executed stored volatile eviction", before, after)
		}
		q := "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)"
		held := bceHeld(t, p, c, q)
		borrower, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, "SELECT * FROM bc_part.pg_buffercache WHERE bufferid=$1::integer", before[0][0])
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
		retained := bcRows(t, old, false, true, bcNames, bcTypes)
		if !reflect.DeepEqual(retained, before) {
			t.Fatal("old cache rows changed across Reset", before, retained)
		}
		for i, col := range metadata {
			if col.Name() != bcNames[i] || col.DatabaseTypeName() != bcTypes[i] {
				t.Fatal("retained metadata mutated", col)
			}
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bceCold(t, p, b, c, "evict_control_heap")
		post(b, c)
		if after := bcTarget(t, p, ob, other, "bc_part.other_control_heap"); !reflect.DeepEqual(peer, after) {
			t.Fatal("selected-database controls retired peer cache", peer, after)
		}
		bceWarm(t, p, b, c, "evict_control_heap")
		wrapped(b, c, "control_evict_relation", true)
		bceWarm(t, p, b, c, "evict_control_heap")
		bceExact(t, bceCall(t, p, b, held, false, false, q, "bc_part.evict_control_heap"), int64(1), int64(0), int64(0))
		bceCold(t, p, b, c, "evict_control_heap")
		bceWarm(t, p, b, c, "evict_control_heap")
		bcUsage(t, p, b, c, "bc_part.evict_control_heap", 1)
		stable := fixture.ExtensionDump(t, p.Ctx, p.Native)
		for _, bad := range []string{dump + "\nINVALID SQL;", dump + "\nCREATE EXTENSION no_evict_provider;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid Restore accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != stable {
				t.Fatal("failed Restore published partial declarations")
			}
			bcUsage(t, p, b, c, "bc_part.evict_control_heap", 1)
			post(b, c)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != stable {
			t.Fatal("canceled Restore changed incumbent state", err)
		}
		bcUsage(t, p, b, c, "bc_part.evict_control_heap", 1)
		post(b, c)

		connector, err := stuntgres.NewDriver().OpenConnector("buffercache-evict-destination")
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
		defer dest.ExecContext(p.Ctx, "RESET ROLE")
		db := b
		db.DB, db.Recorder = pool, rec
		bcDDL(t, p, db, dest, "CREATE TABLE destination_sentinel(k integer)")
		beforeDest := fixture.ExtensionDump(t, p.Ctx, destination)
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err == nil {
			t.Fatal("Restore invented ACL/owner roles")
		}
		if fixture.ExtensionDump(t, p.Ctx, destination) != beforeDest {
			t.Fatal("failed mapped Restore changed destination")
		}
		bcDDL(t, p, db, dest, "CREATE ROLE bc_evict_control_owner", "CREATE ROLE bc_evict_control_reader", "CREATE ROLE bc_evict_control_deny")
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		bceCold(t, p, db, dest, "evict_control_heap")
		post(db, dest)
		bceWarm(t, p, db, dest, "evict_control_heap")
		bceExact(t, bceCall(t, p, db, dest, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.evict_control_heap')"), int64(1), int64(0), int64(0))
		bceCold(t, p, db, dest, "evict_control_heap")
		bceWarm(t, p, db, dest, "evict_control_heap")
		wrapped(db, dest, "control_evict_all", true)
		bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.evict_control_heap")
		bcUsage(t, p, db, dest, "bc_part.evict_control_heap", 1)
		// Independent Driver mutation cannot evict source/peer state.
		bcUsage(t, p, b, c, "bc_part.evict_control_heap", 1)
		bcUsage(t, p, ob, other, "bc_part.other_control_heap", 1)
	})
}
