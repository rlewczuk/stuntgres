package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestReferenceGroupingSetsCancellation(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		q := "SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY GROUPING SETS((a,b),(a),())"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		s := groupingSetsLevel(q, false)
		for _, prepared := range []bool{false, true} {
			if b.pg {
				groupingSetsCancelAdmitted(t, p, b, c, stmt, q, prepared)
			} else {
				// Public precancellation checks publication/held recovery only.
				// Deterministic native executor-loop probes belong to production units.
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled grouped query published cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled grouped query: %v", err)
				}
				result, err := c.ExecContext(ctx, "INSERT INTO gs SELECT a,b,v FROM gs GROUP BY GROUPING SETS((a,b,v),())")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled INSERT published %v: %v", result, err)
				}
			}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			groupingSetsBoth(run, s)
			run(referenceSelect("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		}
	})
}

// Lock admission proves cancellation of an actual grouped query, not time spent
// in hash/set execution. No sleeps, generated-size proxy or pg_sleep surrogate.
func groupingSetsCancelAdmitted(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, stmt *sql.Stmt, q string, prepared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	peer, err := b.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	waiter := referenceLockPID(t, ctx, c)
	blocker := referenceLockPID(t, ctx, peer)
	oid := referenceLockOID(t, ctx, "gs")
	tx, err := peer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE gs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		var o outcome
		if prepared {
			o.rows, o.err = stmt.QueryContext(ctx)
		} else {
			o.rows, o.err = c.QueryContext(ctx, q)
		}
		done <- o
	}()
	joined := false
	defer func() {
		cancel()
		tx.Rollback()
		if !joined {
			select {
			case o := <-done:
				if o.rows != nil {
					o.rows.Close()
				}
			case <-time.After(5 * time.Second):
				t.Error("grouped cancellation worker did not join")
			}
		}
	}()
	referenceLockWaitBehind(t, ctx, waiter, blocker, oid, "AccessShareLock")
	for {
		var active bool
		err := referenceAdmin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND query=$2 AND state='active' AND wait_event_type='Lock')", waiter, q).Scan(&active)
		if err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		runtime.Gosched()
	}
	var signaled bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&signaled); err != nil || !signaled {
		t.Fatalf("cancel admitted grouped query: %t %v", signaled, err)
	}
	var o outcome
	select {
	case o = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("admitted grouped query did not join", ctx.Err())
	}
	if o.rows != nil {
		o.rows.Close()
		t.Fatal("canceled grouped query published cursor")
	}
	referenceAssertDiagnostic(t, o.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
