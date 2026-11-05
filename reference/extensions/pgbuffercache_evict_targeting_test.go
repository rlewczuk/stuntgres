package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionTargeting(t *testing.T) {
	databases := []string{}
	fixture.DatabaseReferenceCleanup(t, &databases)
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bceHeap(t, p, b, c, "target_heap")
		bceHeap(t, p, b, c, "unrelated_heap")
		capacity := bcCapacity(t, p, c)
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "relation_direct", true: "relation_prepared"}[prepared], func(t *testing.T) {
				bceWarm(t, p, b, c, "target_heap")
				other := bceWarm(t, p, b, c, "unrelated_heap")
				got := bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.target_heap")
				bceCounts(t, b, got, capacity, 1, 0)
				bceCold(t, p, b, c, "target_heap")
				after := bcTarget(t, p, b, c, "bc_part.unrelated_heap")
				if len(after) != len(other) || (!b.PG && !reflect.DeepEqual(other, after)) {
					t.Fatal("targeted eviction changed unrelated storage", other, after)
				}
				bceExact(t, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.target_heap"), int64(0), int64(0), int64(0))
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.target_heap")
				bcUsage(t, p, b, c, "bc_part.target_heap", 1)
			})
		}
		t.Run("index_is_not_heap_storage", func(t *testing.T) {
			bcDDL(t, p, b, c, "CREATE INDEX target_hash ON bc_part.target_heap USING hash(k)", "SET enable_seqscan=off")
			defer c.ExecContext(p.Ctx, "RESET enable_seqscan")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.target_heap WHERE k=1")
			index := bcTarget(t, p, b, c, "bc_part.target_hash")
			if len(index) == 0 {
				t.Fatal("genuine index execution did not warm index", index)
			}
			bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.target_heap')"), capacity, 1, 0)
			bceCold(t, p, b, c, "target_heap")
			if after := bcTarget(t, p, b, c, "bc_part.target_hash"); len(after) != len(index) || (!b.PG && !reflect.DeepEqual(after, index)) {
				t.Fatal("heap eviction recursively evicted index", index, after)
			}
			bceCounts(t, b, bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.target_hash')"), capacity, 1, 0)
			bceCold(t, p, b, c, "target_hash")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.target_heap WHERE k=1")
		})
		t.Run("partition_root_does_not_recurse", func(t *testing.T) {
			bcDDL(t, p, b, c,
				"CREATE TABLE bc_part.partition_root(k integer) PARTITION BY RANGE(k)",
				"CREATE TABLE bc_part.partition_child PARTITION OF bc_part.partition_root FOR VALUES FROM (0) TO (2)")
			bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.partition_root VALUES(1)", 1))
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.partition_root")
			before := bcTarget(t, p, b, c, "bc_part.partition_child")
			if len(before) == 0 {
				t.Fatal("actual partition child read absent")
			}
			bceExact(t, bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.partition_root')"), int64(0), int64(0), int64(0))
			if after := bcTarget(t, p, b, c, "bc_part.partition_child"); len(after) != len(before) || (!b.PG && !reflect.DeepEqual(before, after)) {
				t.Fatal("partition root eviction recursively evicted child", before, after)
			}
			bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.partition_child')"), capacity, 1, 0)
			bceCold(t, p, b, c, "partition_child")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.partition_root")
		})
		t.Run("empty_and_nonstorage", func(t *testing.T) {
			bcDDL(t, p, b, c, "CREATE TABLE bc_part.never_read(k integer)", "CREATE VIEW bc_part.nonstorage AS SELECT 1 AS k")
			for _, name := range []string{"never_read", "nonstorage"} {
				bceExact(t, bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part."+name), int64(0), int64(0), int64(0))
			}
		})
		if b.PG {
			t.Run("toast_is_separate_physical_storage", func(t *testing.T) {
				// TOAST is PostgreSQL physical storage, not a fictional native
				// relation. Force external uncompressed data, then really read it.
				bcDDL(t, p, b, c, "CREATE TABLE bc_part.toast_heap(k integer,v text)", "ALTER TABLE bc_part.toast_heap ALTER COLUMN v SET STORAGE EXTERNAL")
				bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.toast_heap VALUES(1,repeat('x',20000))", 1))
				bcBoth(t, p, b, c, fixture.Select("SELECT v=repeat('x',20000) AS same FROM bc_part.toast_heap", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
				var toast string
				if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid::regclass::text FROM pg_class WHERE oid='bc_part.toast_heap'::regclass").Scan(&toast); err != nil {
					t.Fatal(err)
				}
				before := bcTarget(t, p, b, c, toast)
				if len(before) == 0 {
					t.Fatal("actual TOAST read missing")
				}
				bceCounts(t, b, bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.toast_heap')"), capacity, 1, 0)
				if after := bcTarget(t, p, b, c, toast); len(after) != len(before) {
					t.Fatal("heap eviction recursively evicted TOAST", before, after)
				}
				bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", toast), capacity, 1, 0)
			})
		}
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "all_direct", true: "all_prepared"}[prepared], func(t *testing.T) {
				bceWarm(t, p, b, c, "target_heap")
				bceWarm(t, p, b, c, "unrelated_heap")
				n := bceOccupied(t, p, b, c)
				got := bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()")
				want := n
				if b.PG {
					want = 2
				}
				bceCounts(t, b, got, capacity, want, 0)
				bceCold(t, p, b, c, "target_heap")
				bceCold(t, p, b, c, "unrelated_heap")
				if !b.PG {
					bceExact(t, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()"), int64(0), int64(0), int64(0))
				}
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.target_heap")
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.unrelated_heap")
			})
		}
		if b.PG {
			return
		}
		t.Run("all_crosses_database_boundary", func(t *testing.T) {
			databases = append(databases, "bc_evict_other")
			bcDDL(t, p, b, c, "CREATE DATABASE bc_evict_other")
			pool, _, rec := fixture.DatabaseScenarioPool(t, p, false, "bc_evict_other", "")
			other, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			ob := b
			ob.DB, ob.Recorder = pool, rec
			bcDDL(t, p, ob, other, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			bceHeap(t, p, ob, other, "target_heap")
			peer := bceWarm(t, p, ob, other, "target_heap")
			bceWarm(t, p, b, c, "target_heap")
			bceCounts(t, b, bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.target_heap')"), capacity, 1, 0)
			if after := bcTarget(t, p, ob, other, "bc_part.target_heap"); !reflect.DeepEqual(peer, after) {
				t.Fatal("relation selector conflated databases", peer, after)
			}
			n := bceOccupied(t, p, b, c)
			bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()"), capacity, n, 0)
			bceCold(t, p, ob, other, "target_heap")
			bcReadHeap(t, p, ob, other, "SELECT k FROM bc_part.target_heap")
			bcUsage(t, p, ob, other, "bc_part.target_heap", 1)
		})
	})
}
