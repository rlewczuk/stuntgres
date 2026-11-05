package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionOwnership(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bceHeap(t, p, b, c, "lifecycle_kept")
		bceHeap(t, p, b, c, "ownership_other")
		capacity := bcCapacity(t, p, c)
		// Shared public phase: closing direct/prepared readers really releases
		// their source ownership and leaves the known row available to reread.
		for _, prepared := range []bool{false, true} {
			bcRun(t, p, b, c, fixture.Step{Query: "SELECT k FROM bc_part.lifecycle_kept", Prepared: prepared, SelectRows: true, Columns: []string{"k"}, Types: []string{"INT4"}, Rows: [][]any{{int64(1)}}})
			bceCounts(t, b, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.lifecycle_kept')"), capacity, 1, 0)
			bceCold(t, p, b, c, "lifecycle_kept")
		}
		if b.PG {
			// Host ExecutionPause cannot attest PostgreSQL shared-memory pins.
			// The physical pinned rule is independently specified by REL_18_6
			// bufmgr.c EvictUnpinnedBuffer; native uses real retained sources.
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
			return
		}
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct_owners", true: "prepared_owners"}[prepared], func(t *testing.T) {
				bceWarm(t, p, b, c, "lifecycle_kept")
				bceWarm(t, p, b, c, "ownership_other")
				// Two aliases of one session still count as one pinning backend.
				stopA := bcPausedQuery(t, p, b, "SELECT a.k FROM bc_part.lifecycle_kept a CROSS JOIN bc_part.lifecycle_kept z WHERE a.k::text LIKE '1'", prepared)
				bcPins(t, p, b, c, 1)
				stopB := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", !prepared)
				bcPins(t, p, b, c, 2)
				before := bcTarget(t, p, b, c, "bc_part.lifecycle_kept")
				detached := append([]any(nil), before[0]...)
				bceExact(t, bceCall(t, p, b, c, prepared, true, "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", before[0][0]), false, false)
				bceExact(t, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.lifecycle_kept')"), int64(0), int64(0), int64(1))
				n := bceOccupied(t, p, b, c)
				bceExact(t, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_all()"), n-1, int64(0), int64(1))
				bceCold(t, p, b, c, "ownership_other")
				if after := bcTarget(t, p, b, c, "bc_part.lifecycle_kept"); !reflect.DeepEqual(before, after) {
					t.Fatal("pinned eviction changed slot/usage/pins", before, after)
				}
				if err := stopA(); !errors.Is(err, context.Canceled) {
					t.Fatal("first actual source cancel/join", err)
				}
				bcPins(t, p, b, c, 1)
				bceExact(t, bceCall(t, p, b, c, prepared, true, "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", before[0][0]), false, false)
				if err := stopB(); !errors.Is(err, context.Canceled) {
					t.Fatal("second actual source cancel/join", err)
				}
				bcPins(t, p, b, c, 0)
				if detached[8] != int64(2) || !reflect.DeepEqual(detached, before[0]) {
					t.Fatal("detached pinned observation borrowed live ownership", detached, before)
				}
				bceExact(t, bceCall(t, p, b, c, prepared, true, "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)", before[0][0]), true, false)
				bceCold(t, p, b, c, "lifecycle_kept")
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
				bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
			})
		}
	})
}
