package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionMutation(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bceHeap(t, p, b, c, "mutation_heap")
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				bceWarm(t, p, b, c, "mutation_heap")
				bceClean(t, p, b, c)
				before := bcTarget(t, p, b, c, "bc_part.mutation_heap")
				// This one-row heap has exactly one main data block. Additional
				// physical forks do not substitute for the identified data slot.
				var target []any
				for _, row := range before {
					if row[4] == int64(0) && row[5] == int64(0) {
						target = row
					}
				}
				if target == nil || target[6] != false || target[8] != int64(0) {
					t.Fatal("clean unpinned actual target missing", before)
				}
				snapshot := append([]any(nil), target...)
				q := "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)"
				bceExact(t, bceCall(t, p, b, c, prepared, true, q, target[0]), true, false)
				slot := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE bufferid=$1::integer", true, target[0])
				if len(slot) != 1 {
					t.Fatal("evicted slot disappeared from capacity", slot)
				}
				for i := 1; i < len(slot[0]); i++ {
					if slot[0][i] != nil {
						t.Fatal("evicted slot retains occupancy/usage/pins", slot)
					}
				}
				if !reflect.DeepEqual(snapshot, target) {
					t.Fatal("old detached observation mutated", target, snapshot)
				}
				bceExact(t, bceCall(t, p, b, c, prepared, true, q, target[0]), false, false)
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.mutation_heap")
				bcUsage(t, p, b, c, "bc_part.mutation_heap", 1)
				got := bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.mutation_heap")
				bceCounts(t, b, got, bcCapacity(t, p, c), 1, 0)
				bceCold(t, p, b, c, "mutation_heap")
				bceExact(t, bceCall(t, p, b, c, prepared, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)", "bc_part.mutation_heap"), int64(0), int64(0), int64(0))
				bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.mutation_heap")
				bcUsage(t, p, b, c, "bc_part.mutation_heap", 1)
			})
		}
		t.Run("dirty_is_not_blanket_skipped", func(t *testing.T) {
			bcRun(t, p, b, c, fixture.Exec("UPDATE bc_part.mutation_heap SET k=1", 1))
			bceWarm(t, p, b, c, "mutation_heap")
			// No checkpoint: PG may flush its dirty block. Native models
			// only clean logical reads and must perform removal with flush=0.
			got := bceCall(t, p, b, c, false, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.mutation_heap')")
			bceCounts(t, b, got, bcCapacity(t, p, c), 1, 0)
			bceCold(t, p, b, c, "mutation_heap")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.mutation_heap")
		})
		t.Run("rollback_does_not_restore_observational_cache", func(t *testing.T) {
			bceWarm(t, p, b, c, "mutation_heap")
			bcDDL(t, p, b, c, "BEGIN")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			bceCounts(t, b, bceCall(t, p, b, c, true, false, "SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.mutation_heap')"), bcCapacity(t, p, c), 1, 0)
			bcDDL(t, p, b, c, "ROLLBACK")
			bceCold(t, p, b, c, "mutation_heap")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.mutation_heap")
			bcUsage(t, p, b, c, "bc_part.mutation_heap", 1)
		})
		if b.PG {
			return
		}
		t.Run("canceled_calls_do_not_mutate", func(t *testing.T) {
			before := bcTarget(t, p, b, c, "bc_part.mutation_heap")
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{
				"SELECT * FROM bc_part.pg_buffercache_evict($1::integer)",
				"SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)",
				"SELECT * FROM bc_part.pg_buffercache_evict_all()",
			} {
				var args []any
				switch q {
				case "SELECT * FROM bc_part.pg_buffercache_evict($1::integer)":
					args = []any{before[0][0]}
				case "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)":
					args = []any{"bc_part.mutation_heap"}
				}
				rows, err := c.QueryContext(ctx, q, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation", err)
				}
				canceled, err := c.PrepareContext(ctx, q)
				if canceled != nil {
					canceled.Close()
					t.Fatal("pre-canceled Prepare exposed statement")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary Prepare cancellation", err)
				}
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = st.QueryContext(ctx, args...)
				st.Close()
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled prepared query exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary prepared cancellation", err)
				}
			}
			if after := bcTarget(t, p, b, c, "bc_part.mutation_heap"); !reflect.DeepEqual(before, after) {
				t.Fatal("canceled eviction changed actual cache", before, after)
			}
		})
	})
}
