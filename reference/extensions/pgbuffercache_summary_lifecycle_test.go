package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheSummaryLifecycle(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.lifecycle_kept(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.lifecycle_kept VALUES(1)", 1))
		capacity := bcCapacity(t, p, c)
		q := "SELECT * FROM bc_part.pg_buffercache_summary()"
		st, err := c.PrepareContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		bcsNative(t, p, b, held, capacity, 0, nil, [6]int64{capacity})
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
		bcsNative(t, p, b, held, capacity, 1, float64(1), [6]int64{capacity - 1, 1})
		// Repeated partial cursor closes release their detached results, not
		// a heap owner, and cannot increase the observed usage count.
		for _, name := range bcsRoutines {
			query := "SELECT * FROM bc_part." + name + "()"
			cursor, err := c.PrepareContext(p.Ctx, query)
			fixture.State(t, err, "")
			for range 3 {
				rows, err := cursor.QueryContext(p.Ctx)
				fixture.State(t, err, "")
				if !rows.Next() {
					t.Fatal("aggregate cursor empty", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			cursor.Close()
		}
		bcsNative(t, p, b, held, capacity, 1, float64(1), [6]int64{capacity - 1, 1})
		if b.PG {
			return
		}
		borrower, err := b.DB.Conn(p.Ctx)
		fixture.State(t, err, "")
		defer borrower.Close()
		old, err := borrower.QueryContext(p.Ctx, q)
		fixture.State(t, err, "")
		defer old.Close()
		bcMetadata(t, old, false, false, bcsSummaryNames, bcsSummaryTypes)
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
		bcsNative(t, p, b, held, capacity, 1, float64(2), [6]int64{capacity - 1, 0, 1})
		bcsExact(t, bcRows(t, old, false, false, bcsSummaryNames, bcsSummaryTypes), [][]any{{int64(1), capacity - 1, int64(0), int64(0), float64(1)}})
		borrower.Close()
		// These are real reached heap sources paused through the public API.
		// Two sessions own one buffer: pinning_backends=2 but pinned=1.
		stopA := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", false)
		stopB := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", true)
		bcPins(t, p, b, c, 2)
		checkPins := func(want int64) {
			t.Helper()
			summary := bcsQuery(t, p, b, held, q, true, false)
			bcsSummaryBounds(t, summary, capacity, true)
			if summary[0][3] != want {
				t.Fatal("count pinned buffers, not sessions", summary, want)
			}
			usage := bcsQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_usage_counts() ORDER BY usage_count", false, false)
			bcsUsageBounds(t, usage, capacity)
			var total int64
			for _, r := range usage {
				total += r[3].(int64)
			}
			if total != want {
				t.Fatal("bucket pin total", usage, want)
			}
		}
		checkPins(1)
		detached := bcsQuery(t, p, b, held, q, true, false)
		if err := stopA(); !errors.Is(err, context.Canceled) {
			t.Fatal("first admitted source cancellation", err)
		}
		checkPins(1)
		if err := stopB(); !errors.Is(err, context.Canceled) {
			t.Fatal("last admitted source cancellation", err)
		}
		checkPins(0)
		if detached[0][3] != int64(1) {
			t.Fatal("detached snapshot borrowed live pin state", detached)
		}
		for _, query := range []string{q, "SELECT * FROM bc_part.pg_buffercache_usage_counts()"} {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, query)
			if rows != nil {
				rows.Close()
				t.Fatal("pre-canceled Query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary pre-canceled Query", err)
			}
			prepared, err := c.PrepareContext(ctx, query)
			if prepared != nil {
				prepared.Close()
				t.Fatal("pre-canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary pre-canceled Prepare", err)
			}
			prepared, err = c.PrepareContext(p.Ctx, query)
			fixture.State(t, err, "")
			rows, err = prepared.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("pre-canceled held execution exposed rows")
			}
			prepared.Close()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary pre-canceled Execute", err)
			}
		}
		for _, query := range []string{
			"SELECT buffers_used FROM bc_part.pg_buffercache_summary() WHERE buffers_used::text LIKE '1'",
			"SELECT usage_count FROM bc_part.pg_buffercache_usage_counts() WHERE usage_count::text LIKE '1'",
		} {
			for _, prepared := range []bool{false, true} {
				stop := bcPausedQuery(t, p, b, query, prepared)
				if err := stop(); !errors.Is(err, context.Canceled) {
					t.Fatal("admitted aggregate consumer cancellation", err)
				}
				checkPins(0)
			}
		}
		bcsNative(t, p, b, held, capacity, 1, float64(4), [6]int64{capacity - 1, 0, 0, 0, 1})
	})
}
