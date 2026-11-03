package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivityWaitsAndSignals(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		holder := statActivityConn(t, p, b)
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		hpid := statActivityPID(t, p, holder)
		activityExec(t, p, o, "CREATE TABLE activity_wait(v integer)")
		activityExec(t, p, holder, "BEGIN")
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, holder, "LOCK TABLE activity_wait IN ACCESS EXCLUSIVE MODE")
		for _, terminate := range []bool{false, true} {
			activityExec(t, p, peer, "BEGIN")
			q := "LOCK TABLE activity_wait IN ACCESS EXCLUSIVE MODE"
			ctx, cancel := context.WithCancel(p.Ctx)
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				_, err := peer.ExecContext(ctx, q)
				return fixture.RelationOutcome{Err: err}
			})
			joined := false
			func() {
				defer func() {
					cancel()
					if !joined {
						await()
					}
				}()
				statActivityBlocker(t, p, o, pid, hpid)
				r := statActivityAwait(t, p, o, pid, "relation")
				if r.text(14) != "Lock" || r.text(16) != "active" || r.text(20) != q || r[3] != nil {
					t.Fatal("lock wait", r)
				}
				var pending int
				if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 1 {
					t.Fatal("pending owner", pending, err)
				}
				statActivitySignal(t, p, o, pid, terminate)
				out := await()
				joined = true
				if terminate {
					if out.Err == nil {
						t.Fatal("terminated waiter succeeded")
					}
				} else {
					fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
					statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle in transaction (aborted)", q)
					if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 0 {
						t.Fatal("canceled owner retained queue entry", pending, err)
					}
					var blockers string
					if err := o.QueryRowContext(p.Ctx, "SELECT pg_blocking_pids($1)::text", pid).Scan(&blockers); err != nil || blockers != "{}" {
						t.Fatal("canceled blocker ownership", blockers, err)
					}
					var grants int
					if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND locktype='relation' AND relation='activity_wait'::regclass AND mode='AccessExclusiveLock'", hpid).Scan(&grants); err != nil || grants != 1 {
						t.Fatal("waiter cancellation removed holder grant", grants, err)
					}
					if holderRow := statActivityGet(t, p, o, hpid, true); holderRow.text(16) != "idle in transaction" {
						t.Fatal("canceling waiter changed holder", holderRow)
					}
					activityExec(t, p, peer, "ROLLBACK")
				}
			}()
		}
		statActivityRetired(t, p, o, pid)
		if statActivityGet(t, p, o, hpid, true).text(2) == "<NULL>" {
			t.Fatal("holder lost")
		}
		activityExec(t, p, holder, "ROLLBACK")
		peer = statActivityConn(t, p, b)
		pid = statActivityPID(t, p, peer)
		activityExec(t, p, holder, "SELECT pg_advisory_lock(912376::bigint)")
		defer holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock_all()")
		ctx2, cancel2 := context.WithCancel(p.Ctx)
		defer cancel2()
		await := fixture.LockOperation(t, ctx2, func(ctx context.Context) fixture.RelationOutcome {
			_, err := peer.ExecContext(ctx, "SELECT pg_advisory_lock(912376::bigint)")
			return fixture.RelationOutcome{Err: err}
		})
		joined := false
		defer func() {
			cancel2()
			if !joined {
				await()
			}
		}()
		statActivityBlocker(t, p, o, pid, hpid)
		r := statActivityAwait(t, p, o, pid, "advisory")
		if r.text(14) != "Lock" {
			t.Fatal(r)
		}
		statActivitySignal(t, p, o, pid, false)
		out := await()
		joined = true
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		r = statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, r, "idle", "SELECT pg_advisory_lock(912376::bigint)")
		var advisoryPending int
		if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&advisoryPending); err != nil || advisoryPending != 0 {
			t.Fatal("canceled advisory request retained queue entry", advisoryPending, err)
		}
		// This is the actual timer-owning SQL builtin, not a static event definition.
		sleepCtx, sleepCancel := context.WithCancel(p.Ctx)
		defer sleepCancel()
		sleep := fixture.LockOperation(t, sleepCtx, func(ctx context.Context) fixture.RelationOutcome {
			_, err := peer.ExecContext(ctx, "SELECT pg_sleep(600)")
			return fixture.RelationOutcome{Err: err}
		})
		slept := false
		defer func() {
			sleepCancel()
			if !slept {
				sleep()
			}
		}()
		r = statActivityAwait(t, p, o, pid, "PgSleep")
		if r.text(14) != "Timeout" || r.text(16) != "active" {
			t.Fatal("sleep ownership", r)
		}
		statActivitySignal(t, p, o, pid, false)
		out = sleep()
		slept = true
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		r = statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, r, "idle", "SELECT pg_sleep(600)")
		// A bounded context cancellation is still the adapter's ordinary error boundary.
		if !b.PG {
			canceled, stop := context.WithCancel(p.Ctx)
			done := fixture.LockOperation(t, canceled, func(ctx context.Context) fixture.RelationOutcome {
				_, err := peer.ExecContext(ctx, "SELECT pg_sleep(600)")
				return fixture.RelationOutcome{Err: err}
			})
			statActivityAwait(t, p, o, pid, "PgSleep")
			stop()
			out := done()
			var structured interface{ SQLState() string }
			if !errors.Is(out.Err, context.Canceled) || errors.As(out.Err, &structured) {
				t.Fatalf("ordinary context error %T %v", out.Err, out.Err)
			}
		}
	})
}
