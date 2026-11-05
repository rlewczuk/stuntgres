package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"time"

	"stuntgres/reference/fixture"
)

// The caller supplies a cold physical decoder: it has never opened t. The
// catalog lock parks RelationBuildTupleDesc inside actual decoding, after the
// slot is acquired but before the change callback. Locking t itself would not
// work: logical decoding opens historical relations without a relation lock.
func tdCancelAdmitted(s *fixture.RoleMembershipScenario, db *sql.DB, decoder *sql.Conn, held *sql.Stmt, query string) {
	s.T.Helper()
	ctx, cancel := context.WithTimeout(s.P.Ctx, fixture.Liveness)
	defer cancel()
	observer, err := db.Conn(ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer observer.Close()
	holder, err := db.Conn(ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer holder.Close()
	waiter := fixture.LockPID(s.T, ctx, decoder)
	blocker := fixture.LockPID(s.T, ctx, holder)
	attribute := fixture.LockOID(s.T, ctx, "pg_catalog.pg_attribute")
	monitor, err := observer.PrepareContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$3 AND mode='AccessShareLock' AND NOT granted)
		AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$2 AND locktype='relation' AND relation=$3 AND mode='AccessExclusiveLock' AND granted)
		AND $2=ANY(pg_blocking_pids($1))
		AND EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name='td_slot' AND active AND active_pid=$1)
		AND EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND query=$4 AND wait_event_type='Lock')`)
	if err != nil {
		s.T.Fatal(err)
	}
	defer monitor.Close()
	// Prepare and execute every observer query before taking a catalog lock;
	// otherwise the admission observer could itself need the blocked descriptor.
	var admitted bool
	if err := monitor.QueryRowContext(ctx, waiter, blocker, attribute, query).Scan(&admitted); err != nil || admitted {
		s.T.Fatal("decode unexpectedly admitted before launch", admitted, err)
	}
	signal, err := observer.PrepareContext(ctx, "SELECT pg_cancel_backend($1)")
	if err != nil {
		s.T.Fatal(err)
	}
	defer signal.Close()
	var signaled bool
	if err := signal.QueryRowContext(ctx, 0).Scan(&signaled); err != nil || signaled {
		s.T.Fatal("warming cancellation signal", signaled, err)
	}
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		s.T.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "LOCK TABLE pg_catalog.pg_attribute IN ACCESS EXCLUSIVE MODE"); err != nil {
		s.T.Fatal(err)
	}
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	joined := false
	defer func() {
		cancel()
		tx.Rollback()
		if !joined {
			select {
			case out := <-done:
				if out.rows != nil {
					out.rows.Close()
				}
			case <-time.After(fixture.Liveness):
				s.T.Error("decode cancellation worker did not join")
			}
		}
	}()
	go func() {
		var out outcome
		if s.Prepared {
			out.rows, out.err = held.QueryContext(ctx)
		} else {
			out.rows, out.err = decoder.QueryContext(ctx, query)
		}
		done <- out
	}()
	for {
		if err := monitor.QueryRowContext(ctx, waiter, blocker, attribute, query).Scan(&admitted); err != nil {
			s.T.Fatal("decode admission observer", err)
		}
		if admitted {
			break
		}
		select {
		case out := <-done:
			joined = true
			if out.rows != nil {
				out.rows.Close()
			}
			s.T.Fatal("decode completed before catalog/slot admission", out.err)
		case <-ctx.Done():
			s.T.Fatal("decode admission deadline", ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	if err := signal.QueryRowContext(ctx, waiter).Scan(&signaled); err != nil || !signaled {
		s.T.Fatal("cancel admitted decoder", signaled, err)
	}
	select {
	case out := <-done:
		joined = true
		if out.rows != nil {
			out.rows.Close()
			s.T.Fatal("cancelled decoder exposed rows")
		}
		fixture.AssertDiagnostic(s.T, out.err, &fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
			Message: "canceling statement due to user request",
		})
	case <-ctx.Done():
		s.T.Fatal("admitted decoder did not join", ctx.Err())
	}
	if err := tx.Rollback(); err != nil {
		s.T.Fatal(err)
	}
}
