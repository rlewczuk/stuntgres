package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func (f *fullVacuumCase) start(q string) (context.CancelFunc, func() fixture.RelationOutcome, <-chan struct{}) {
	f.t.Helper()
	var observer *sql.Conn
	var pid int
	if f.b.PG {
		observer = vacuumConn(f.t, f.p, f.b)
		pid = fixture.LockPID(f.t, f.p.Ctx, f.c)
	}
	var stmt *sql.Stmt
	if f.prepared {
		h, ok := f.held[q]
		if !ok {
			h = vacuumHeld(f.t, f.p, f.c, q)
			f.held[q] = h
		}
		stmt = h.Statement
	}
	ctx, cancel := context.WithCancel(f.p.Ctx)
	f.t.Cleanup(cancel)
	finished := make(chan struct{})
	join := fixture.LockOperation(f.t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		var o fixture.RelationOutcome
		if stmt != nil {
			o.Result, o.Err = stmt.ExecContext(ctx)
		} else {
			o.Result, o.Err = f.c.ExecContext(ctx, q)
		}
		return o
	})
	// Join before LockOperation's context cancellation and held-statement
	// cleanup. lib/pq context cancellation can destroy a prepared session;
	// server cancellation preserves it even when an assertion fails mid-work.
	joined := false
	var outcome fixture.RelationOutcome
	await := func() fixture.RelationOutcome {
		if !joined {
			outcome = join()
			joined = true
		}
		return outcome
	}
	f.t.Cleanup(func() {
		if !joined {
			select {
			case <-finished:
			default:
				if f.b.PG {
					var ok bool
					if err := observer.QueryRowContext(f.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
						f.t.Error("worker cleanup cancellation", ok, err)
					}
				} else {
					cancel()
				}
			}
			o := await()
			if o.Rows != nil {
				o.Rows.Close()
			}
		}
		cancel()
	})
	return cancel, await, finished
}

func fullCancel(f *fullVacuumCase, observer *sql.Conn, pid int, cancel context.CancelFunc, join func() fixture.RelationOutcome, executionContext string) {
	f.t.Helper()
	if f.b.PG {
		var ok bool
		if err := observer.QueryRowContext(f.p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
			f.t.Fatal("server cancellation", ok, err)
		}
	} else {
		cancel()
	}
	o := join()
	if f.b.PG {
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		want.Context = executionContext
		fixture.AssertDiagnostic(f.t, o.Err, &want)
	} else if !errors.Is(o.Err, context.Canceled) {
		f.t.Fatal("native active cancellation", o.Err)
	}
}

func TestReferenceVacuumFullLocking(t *testing.T) {
	// Literal documented eight-holder matrix, not the engine conflict table.
	for _, mode := range []string{"ACCESS SHARE", "ROW SHARE", "ROW EXCLUSIVE", "SHARE UPDATE EXCLUSIVE", "SHARE", "SHARE ROW EXCLUSIVE", "EXCLUSIVE", "ACCESS EXCLUSIVE"} {
		t.Run(mode, func(t *testing.T) {
			fullVacuumCases(t, func(f *fullVacuumCase) {
				f.run(fixture.Exec("CREATE TABLE full_locks(id INT)", 0))
				f.run(fixture.Exec("INSERT INTO full_locks VALUES(1),(2)", 2))
				holder := vacuumConn(f.t, f.p, f.b)
				waiter, blocker := fixture.LockPID(f.t, f.p.Ctx, f.c), fixture.LockPID(f.t, f.p.Ctx, holder)
				before := f.locator("full_locks")
				vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(f.p.Ctx, "ROLLBACK")
				vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("LOCK TABLE full_locks IN "+mode+" MODE", 0))
				_, join, finished := f.start("VACUUM FULL full_locks")
				wanted := "AccessExclusiveLock"
				// Root-name resolution itself needs AS before work can request AX.
				if mode == "ACCESS EXCLUSIVE" {
					wanted = "AccessShareLock"
				}
				fixture.RelationLockPending(f.t, f.p, f.b, waiter, blocker, before.oid, wanted, finished)
				observer := vacuumConn(f.t, f.p, f.b)
				vacuumRun(f.t, f.p, f.b, observer, fixture.Select("SELECT count(*) FROM pg_stat_progress_cluster WHERE pid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, waiter))
				vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("COMMIT", 0))
				o := join()
				fixture.DeleteReferenceCount(f.t, o.Result, o.Err, 0)
				f.rewritten("full_locks", before)
				f.emptyProgress()
			})
		})
	}
}

func TestReferenceVacuumFullCancellation(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE full_cancel(id INT PRIMARY KEY)", 0))
		f.run(fixture.Exec("INSERT INTO full_cancel VALUES(1),(2)", 2))
		f.run(fixture.Exec("DELETE FROM full_cancel WHERE id=1", 1))
		holder := vacuumConn(f.t, f.p, f.b)
		observer := vacuumConn(f.t, f.p, f.b)
		pid, blocker := fixture.LockPID(f.t, f.p.Ctx, f.c), fixture.LockPID(f.t, f.p.Ctx, holder)
		before, ix := f.locator("full_cancel"), f.locator("full_cancel_pkey")
		vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("BEGIN", 0))
		defer holder.ExecContext(f.p.Ctx, "ROLLBACK")
		// A genuine SELECT AS lock blocks FULL, not ordinary VACUUM.
		vacuumRows(f.t, f.p, f.b, holder, "full_cancel", 2)
		cancel, join, finished := f.start("VACUUM FULL full_cancel")
		fixture.RelationLockPending(f.t, f.p, f.b, pid, blocker, before.oid, "AccessExclusiveLock", finished)
		fullCancel(f, observer, pid, cancel, join, "")
		f.unchanged("full_cancel", before)
		f.unchanged("full_cancel_pkey", ix)
		f.emptyProgress()
		// Precise lock timeout independently establishes the failed work wait.
		f.run(fixture.Exec("SET lock_timeout='10ms'", 0))
		f.command(fixture.ErrorDiagnostic("VACUUM FULL full_cancel", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
		f.run(fixture.Exec("RESET lock_timeout", 0))
		f.run(fixture.Exec("SET statement_timeout='100ms'", 0))
		f.command(fixture.ErrorDiagnostic("VACUUM FULL full_cancel", fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")))
		f.run(fixture.Exec("RESET statement_timeout", 0))
		f.unchanged("full_cancel", before)
		vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("ROLLBACK", 0))
		f.command(fixture.Exec("VACUUM FULL full_cancel", 0))
		f.rewritten("full_cancel", before)
		f.rewritten("full_cancel_pkey", ix)
		vacuumRows(f.t, f.p, f.b, f.c, "full_cancel", 2)
		fullActiveRewrite(f, true)
		fullTargetTurnover(f, true)
	}, "full_hidden_worker")
}

func fullAwaitAdvisory(f *fullVacuumCase, observer *sql.Conn, waiter, holder int, finished <-chan struct{}) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := observer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))", waiter, holder).Scan(&waiting); err != nil {
			f.t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-finished:
			f.t.Fatal("rewrite finished before actual expression wait")
		case <-ctx.Done():
			f.t.Fatal(ctx.Err())
		default:
			runtime.Gosched()
		}
	}
}
