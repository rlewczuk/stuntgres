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

func TestReferencePgBuffercacheSummaryControls(t *testing.T) {
	roles := []string{"bc_sum_control_owner", "bc_sum_control_reader", "bc_sum_control_deny"}
	fixture.RoleReferenceCleanup(t, roles)
	fixture.SchemaReferenceCleanup(t, "bc_sum_moved")
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE ROLE bc_sum_control_owner", "CREATE ROLE bc_sum_control_reader", "CREATE ROLE bc_sum_control_deny",
			"CREATE SCHEMA bc_moved", "GRANT USAGE ON SCHEMA bc_part,bc_moved TO bc_sum_control_owner,bc_sum_control_reader,bc_sum_control_deny",
			"CREATE TABLE bc_part.summary_control_heap(k integer)",
			"CREATE VIEW bc_part.saved_summary AS SELECT * FROM bc_part.pg_buffercache_summary()",
			"CREATE VIEW bc_part.saved_scalar AS SELECT (bc_part.pg_buffercache_summary()).buffers_used AS used",
			"CREATE VIEW bc_part.saved_usage AS SELECT * FROM bc_part.pg_buffercache_usage_counts()")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.summary_control_heap VALUES(1)", 1))
		for _, name := range bcsRoutines {
			bcDDL(t, p, b, c, "ALTER FUNCTION bc_part."+name+"() OWNER TO bc_sum_control_owner", "GRANT EXECUTE ON FUNCTION bc_part."+name+"() TO bc_sum_control_reader", "ALTER FUNCTION bc_part."+name+"() RENAME TO "+name+"_kept", "ALTER FUNCTION bc_part."+name+"_kept() SET SCHEMA bc_moved")
		}
		bcDDL(t, p, b, c, "ALTER SCHEMA bc_moved RENAME TO bc_sum_moved")
		capacity := bcCapacity(t, p, c)
		post := func(target *sql.Conn, backend fixture.Backend) {
			t.Helper()
			defer target.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			for i, name := range bcsRoutines {
				sig := "bc_sum_moved." + name + "_kept()"
				bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid=$1::regprocedure", []string{"owner"}, []string{"NAME"}, [][]any{{roles[0]}}, sig))
				bcsInitialPrivilege(t, p, backend, target, sig, true)
				bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::regprocedure AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, sig))
				view := []string{"saved_summary", "saved_usage"}[i]
				bcBoth(t, p, backend, target, fixture.Select("SELECT pg_get_viewdef($1::regclass) LIKE $2::text AS renamed", []string{"renamed"}, []string{"BOOL"}, [][]any{{true}}, "bc_part."+view, "%"+name+"_kept%"))
			}
			bcsSummaryBounds(t, bcsQuery(t, p, backend, target, "SELECT * FROM bc_part.saved_summary", true, true), capacity, false)
			bcsUsageBounds(t, bcsQuery(t, p, backend, target, "SELECT * FROM bc_part.saved_usage ORDER BY usage_count", false, true), capacity)
			bcBoth(t, p, backend, target, fixture.Select("SELECT used>=0 AS valid FROM bc_part.saved_scalar", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}))
			// Prior pages and eviction declarations remain genuine and callable.
			bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
			bcBoth(t, p, backend, target, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all')", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			for _, role := range []string{roles[1], roles[2]} {
				bcDDL(t, p, backend, target, "SET SESSION AUTHORIZATION "+role)
				for i, name := range bcsRoutines {
					q := "SELECT count(*) AS n FROM bc_sum_moved." + name + "_kept()"
					if role == roles[1] {
						n := int64(1)
						if i == 1 {
							n = 6
						}
						bcBoth(t, p, backend, target, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}))
					} else {
						bcFailure(t, p, backend, target, bcError(q, "42501", "permission denied for function "+name+"_kept"), "execute")
					}
				}
				bcDDL(t, p, backend, target, "RESET SESSION AUTHORIZATION")
			}
		}
		post(c, b)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.summary_control_heap")
		if b.PG {
			return
		}
		// A peer database's genuine shared-cluster contribution survives target
		// controls; restore is cold only for the replaced database.
		databases = append(databases, "bc_sum_peer")
		bcDDL(t, p, b, c, "CREATE DATABASE bc_sum_peer")
		peerPool, _, peerRecorder := fixture.DatabaseScenarioPool(t, p, false, "bc_sum_peer", "")
		peer, err := peerPool.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer peer.Close()
		pb := b
		pb.DB, pb.Recorder = peerPool, peerRecorder
		bcDDL(t, p, pb, peer, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part", "CREATE TABLE bc_part.summary_peer_heap(k integer)")
		bcRun(t, p, pb, peer, fixture.Exec("INSERT INTO bc_part.summary_peer_heap VALUES(1)", 1))
		bcReadHeap(t, p, pb, peer, "SELECT k FROM bc_part.summary_peer_heap")
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		q := "SELECT * FROM bc_part.saved_summary"
		st, err := c.PrepareContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		borrower, err := b.DB.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer old.Close()
		bcMetadata(t, old, false, true, bcsSummaryNames, bcsSummaryTypes)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		bcsExact(t, bcRows(t, old, false, true, bcsSummaryNames, bcsSummaryTypes), [][]any{{int64(2), capacity - 2, int64(0), int64(0), float64(1)}})
		borrower.Close()
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		bcsNative(t, p, pb, peer, capacity, 1, float64(1), [6]int64{capacity - 1, 1})
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(c, b)
		bcsExact(t, bcsQuery(t, p, b, held, q, true, true), [][]any{{int64(1), capacity - 1, int64(0), int64(0), float64(1)}})
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.summary_control_heap")
		bcsExact(t, bcsQuery(t, p, b, held, q, true, true), [][]any{{int64(2), capacity - 2, int64(0), int64(0), float64(1)}})
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid Restore accepted")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed/canceled Restore changed catalog", err)
		}
		bcsExact(t, bcsQuery(t, p, b, held, q, true, true), [][]any{{int64(2), capacity - 2, int64(0), int64(0), float64(1)}})
		connector, err := stuntgres.NewDriver().OpenConnector("summary-independent-control")
		fixture.State(t, err, "")
		raw := sql.OpenDB(connector)
		defer raw.Close()
		pool, rec := fixture.RecordedNativePool(t, connector)
		dest, err := pool.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer dest.Close()
		db := b
		db.DB, db.Recorder = pool, rec
		for _, role := range roles {
			bcDDL(t, p, db, dest, "CREATE ROLE "+role)
		}
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(dest, db)
		bcsExact(t, bcsQuery(t, p, db, dest, q, true, true), [][]any{{int64(0), capacity, int64(0), int64(0), nil}})
		bcReadHeap(t, p, db, dest, "SELECT k FROM bc_part.summary_control_heap")
		bcsExact(t, bcsQuery(t, p, db, dest, q, true, true), [][]any{{int64(1), capacity - 1, int64(0), int64(0), float64(1)}})
	})
}
