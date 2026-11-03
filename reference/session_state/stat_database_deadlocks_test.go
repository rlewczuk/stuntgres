package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseDeadlocks(t *testing.T) {
	statDatabaseScenario(t, "deadlocks", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		target := create("target")
		statDatabaseSetup(t, p, target, observer)
		setup, retireSetup := statDatabaseConn(t, p, target)
		statsExec(t, p, target, setup, "CREATE TABLE stat_db_lock_x(id integer)", 0)
		statsExec(t, p, target, setup, "CREATE TABLE stat_db_lock_y(id integer)", 0)
		retireSetup()
		base := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == 2 })
		a, endA := statDatabaseConn(t, p, target)
		bb, endB := statDatabaseConn(t, p, target)
		var pidA, pidB int
		var relation int64
		if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pidA); err != nil {
			t.Fatal(err)
		}
		if err := bb.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pidB); err != nil {
			t.Fatal(err)
		}
		if err := bb.QueryRowContext(p.Ctx, "SELECT 'stat_db_lock_y'::regclass::bigint").Scan(&relation); err != nil {
			t.Fatal(err)
		}
		statsExec(t, p, target, a, "BEGIN", 0)
		statsExec(t, p, target, bb, "BEGIN", 0)
		statsExec(t, p, target, a, "LOCK TABLE stat_db_lock_x IN ACCESS EXCLUSIVE MODE", 0)
		statsExec(t, p, target, bb, "LOCK TABLE stat_db_lock_y IN ACCESS EXCLUSIVE MODE", 0)
		leftFinished := make(chan struct{})
		left := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(leftFinished)
			_, err := a.ExecContext(ctx, "LOCK TABLE stat_db_lock_y IN ACCESS EXCLUSIVE MODE")
			return fixture.RelationOutcome{Err: err}
		})
		fixture.RelationLockPending(t, p, target, pidA, pidB, relation, "AccessExclusiveLock", leftFinished)
		right := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			_, err := bb.ExecContext(ctx, "LOCK TABLE stat_db_lock_x IN ACCESS EXCLUSIVE MODE")
			return fixture.RelationOutcome{Err: err}
		})
		errA, errB := left().Err, right().Err
		if (errA == nil) == (errB == nil) {
			t.Fatal("need one real selected victim", errA, errB)
		}
		victimError := errA
		if victimError == nil {
			victimError = errB
		}
		diagnostic := fixture.ExtractDiagnostic(t, victimError)
		if diagnostic.Message != "deadlock detected" || diagnostic.Hint != "See server log for query details." {
			t.Fatal(diagnostic)
		}
		if errA != nil {
			fixture.State(t, errA, "40P01")
			statsExec(t, p, target, a, "ROLLBACK", 0)
			statsExec(t, p, target, bb, "COMMIT", 0)
		} else {
			fixture.State(t, errB, "40P01")
			statsExec(t, p, target, bb, "ROLLBACK", 0)
			statsExec(t, p, target, a, "COMMIT", 0)
		}
		endA()
		endB()
		after := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Deadlocks == base.Deadlocks+1 })
		if after.Rollback != base.Rollback+1 {
			t.Fatal("victim abort not exactly once", base, after)
		}
		// SQLSTATE text is not the event authority.
		user, endUser := statDatabaseConn(t, p, target)
		_, err := user.ExecContext(p.Ctx, "DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='40P01', MESSAGE='not a lock cycle'; END $$")
		fixture.State(t, err, "40P01")
		endUser()
		control := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == after.Sessions+1 })
		if control.Deadlocks != after.Deadlocks {
			t.Fatal("user SQLSTATE counted as deadlock", after, control)
		}
		for _, kind := range []string{"timeout", "cancel"} {
			holder, endHolder := statDatabaseConn(t, p, target)
			waiter, endWaiter := statDatabaseConn(t, p, target)
			var waiterPID, holderPID int
			var lockedRelation int64
			if err := waiter.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&waiterPID); err != nil {
				t.Fatal(err)
			}
			if err := holder.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
				t.Fatal(err)
			}
			if err := holder.QueryRowContext(p.Ctx, "SELECT 'stat_db_lock_x'::regclass::bigint").Scan(&lockedRelation); err != nil {
				t.Fatal(err)
			}
			statsExec(t, p, target, holder, "BEGIN", 0)
			statsExec(t, p, target, holder, "LOCK TABLE stat_db_lock_x IN ACCESS EXCLUSIVE MODE", 0)
			if kind == "timeout" {
				statsExec(t, p, target, waiter, "SET lock_timeout='5ms'", 0)
			}
			statsExec(t, p, target, waiter, "BEGIN", 0)
			finished := make(chan struct{})
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				_, err := waiter.ExecContext(ctx, "LOCK TABLE stat_db_lock_x IN ACCESS EXCLUSIVE MODE")
				return fixture.RelationOutcome{Err: err}
			})
			if kind == "cancel" {
				fixture.RelationLockPending(t, p, target, waiterPID, holderPID, lockedRelation, "AccessExclusiveLock", finished)
				var sent bool
				if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&sent); err != nil || !sent {
					t.Fatal(sent, err)
				}
			}
			result := join().Err
			code := "55P03"
			if kind == "cancel" {
				code = "57014"
			}
			fixture.State(t, result, code)
			statsExec(t, p, target, waiter, "ROLLBACK", 0)
			statsExec(t, p, target, holder, "COMMIT", 0)
			fixture.RunStep(t, p.Ctx, waiter, target.Recorder, fixture.Select("SELECT 42 AS live", []string{"live"}, []string{"INT4"}, [][]any{{int64(42)}}))
			endWaiter()
			endHolder()
		}
		final := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == control.Sessions+4 })
		if final.Deadlocks != control.Deadlocks || final.Abandoned != control.Abandoned || final.Fatal != control.Fatal || final.Killed != control.Killed {
			t.Fatal("timeout/cancel became deadlock/disconnect", control, final)
		}
	})
}
