package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMATopology(t *testing.T) {
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcNUMAScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		// First demanded released call: the fixed source-informed init error is
		// an expectation, not a catch-all fallback learned from this execution.
		bcNUMARead(t, p, b, c, "SELECT * FROM "+bcNUMAFunction(), false, capacity)
		bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
		bcNUMAEmpty(t, p, b, c, "bc_part.pg_buffercache_numa", true)
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.numa_heap(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.numa_heap VALUES(1)", 1))
		if !b.PG {
			bcUsage(t, p, b, c, "bc_part.numa_heap", 0)
			bcUnused(t, p, b, c)
		}
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.numa_heap")
		bcUsage(t, p, b, c, "bc_part.numa_heap", 1)
		q := "SELECT * FROM bc_part.pg_buffercache ORDER BY bufferid"
		before := bcQuery(t, p, b, c, q, true)
		for _, from := range []string{"bc_part.pg_buffercache_numa", bcNUMAFunction()} {
			query := "SELECT * FROM " + from
			st, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: st, SQL: query}, fixture.HeldStatement{Executor: c, Statement: st, SQL: query}} {
				bcNUMARead(t, p, b, source, query, from == "bc_part.pg_buffercache_numa", capacity)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			bcNUMABoth(t, p, b, c, bcNUMACount("SELECT count(DISTINCT bufferid) AS n FROM "+from, capacity))
		}
		if !b.PG {
			bcNUMAUnchanged(t, before, bcQuery(t, p, b, c, q, true))
			for _, tc := range []struct { order string; want []any }{
				{"ASC", []any{int64(1), int64(0), int64(0)}},
				{"DESC", []any{capacity, capacity - 1, int64(0)}},
			} {
				bcBoth(t, p, b, c, fixture.Select("SELECT * FROM bc_part.pg_buffercache_numa ORDER BY bufferid "+tc.order+" LIMIT 1", bcNUMANames, bcNUMATypes, [][]any{tc.want}))
			}
		}
		// Every genuine ordinary identity has NUMA rows, including a controlled
		// occupied heap and genuinely unused buffers. No host-layout equality.
		node, space, database := bcStorage(t, p, c, "bc_part.numa_heap")
		join := "SELECT count(DISTINCT n.bufferid) AS n FROM bc_part.pg_buffercache_numa n JOIN bc_part.pg_buffercache c ON c.bufferid=n.bufferid"
		bcNUMABoth(t, p, b, c, bcNUMACount(join, capacity))
		occupied := bcTarget(t, p, b, c, "bc_part.numa_heap")
		s := bcNUMACount(join+" WHERE c.relfilenode=$1::oid AND c.reltablespace=$2::oid AND c.reldatabase=$3::oid", int64(len(occupied)))
		s.Args = []any{node, space, database}
		bcNUMABoth(t, p, b, c, s)
		if !b.PG {
			var unused int64
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM bc_part.pg_buffercache WHERE relfilenode IS NULL").Scan(&unused); err != nil || unused <= 0 {
				t.Fatal("genuine unused capacity required", unused, err)
			}
			bcBoth(t, p, b, c, bcNUMACount(join+" WHERE c.relfilenode IS NULL AND n.os_page_num=n.bufferid-1 AND n.numa_node=0", unused))
			bcNUMAUnchanged(t, before, bcQuery(t, p, b, c, q, true))
		} else {
			return
		}

		t.Run("native_cluster_and_driver_isolation", func(t *testing.T) {
			databases = append(databases, "bc_numa_peer")
			bcDDL(t, p, b, c, "CREATE DATABASE bc_numa_peer")
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, false, "bc_numa_peer", "")
			peer, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			pb := b
			pb.DB, pb.Recorder = pool, recorder
			bcDDL(t, p, pb, peer, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			if got := bcCapacity(t, p, peer); got != capacity {
				t.Fatal("same cluster capacity differs", got, capacity)
			}
			local := bcNUMARead(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_numa", true, capacity)
			remote := bcNUMARead(t, p, pb, peer, "SELECT * FROM bc_part.pg_buffercache_numa", true, capacity)
			// ORDER BY makes the comparison about identity, not source order.
			local = bcNUMARead(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_numa ORDER BY bufferid", true, capacity)
			remote = bcNUMARead(t, p, pb, peer, "SELECT * FROM bc_part.pg_buffercache_numa ORDER BY bufferid", true, capacity)
			if !reflect.DeepEqual(local, remote) {
				t.Fatal("same-cluster NUMA namespace", local, remote)
			}
			bcBoth(t, p, pb, peer, fixture.Select("SELECT bufferid FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", []string{"bufferid"}, []string{"INT4"}, [][]any{{occupied[0][0]}}, node, space, database))
			connector, err := stuntgres.NewDriver().OpenConnector("numa-independent-driver")
			if err != nil {
				t.Fatal(err)
			}
			isolated, rec := fixture.RecordedNativePool(t, connector)
			ic, err := isolated.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer ic.Close()
			ib := b
			ib.DB, ib.Recorder = isolated, rec
			bcDDL(t, p, ib, ic, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			bcNUMARead(t, p, ib, ic, "SELECT * FROM bc_part.pg_buffercache_numa", true, bcCapacity(t, p, ic))
			bcBoth(t, p, ib, ic, bcNUMACount("SELECT count(*) AS n FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL", 0))
			bcUsage(t, p, b, c, "bc_part.numa_heap", 1)
		})
	})
}
