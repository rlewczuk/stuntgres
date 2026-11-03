package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivityFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"activity_smoke_unrelated"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		holder := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		hpid := statActivityPID(t, p, holder)
		activityExec(t, p, o, "CREATE TABLE activity_smoke(v integer)")
		activityExec(t, p, o, "CREATE ROLE activity_smoke_unrelated LOGIN PASSWORD 'role-attributes-fixture'")
		activityExec(t, p, peer, "SELECT 37 /* activity feature smoke */")
		rows, err := o.QueryContext(p.Ctx, statActivitySelect, pid)
		if err != nil {
			t.Fatal(err)
		}
		statActivityMetadata(t, rows, b.PG)
		idle, ok := statActivityRead(t, rows)
		if !ok {
			t.Fatal("missing logical backend")
		}
		statActivityState(t, b.PG, idle, "idle", "SELECT 37 /* activity feature smoke */")
		if !b.PG {
			for _, i := range []int{3, 7, 8, 9} {
				if idle[i] != nil {
					t.Fatal("invented process/endpoint", idle)
				}
			}
		}
		activityExec(t, p, holder, "BEGIN")
		defer holder.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, holder, "LOCK TABLE activity_smoke IN ACCESS EXCLUSIVE MODE")
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		q := "LOCK TABLE activity_smoke IN ACCESS EXCLUSIVE MODE"
		start := func() (func() fixture.RelationOutcome, context.CancelFunc) {
			ctx, cancel := context.WithCancel(p.Ctx)
			return fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				_, err := peer.ExecContext(ctx, q)
				return fixture.RelationOutcome{Err: err}
			}), cancel
		}
		await, cancel := start()
		joined := false
		defer func() {
			cancel()
			if !joined {
				await()
			}
		}()
		statActivityBlocker(t, p, o, pid, hpid)
		active := statActivityAwait(t, p, o, pid, "relation")
		if active.text(16) != "active" || active.text(14) != "Lock" || active.text(20) != q || active[3] != nil {
			t.Fatal("active lock smoke", active)
		}
		pool := fixture.RoleAttributesPool(t, p, b, "activity_smoke_unrelated")
		unrelated, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer unrelated.Close()
		statActivityDenied(t, active, statActivityGet(t, p, unrelated, pid, true))
		activityExec(t, p, holder, "ROLLBACK")
		out := await()
		joined = true
		cancel()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle in transaction", q)
		_, err = peer.ExecContext(p.Ctx, "SELECT 1/0")
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
		aborted := statActivityGet(t, p, o, pid, true)
		statActivityState(t, b.PG, aborted, "idle in transaction (aborted)", "SELECT 1/0")
		if aborted[11] != nil {
			t.Fatal("abort retained xact_start")
		}
		activityExec(t, p, peer, "ROLLBACK")
		activityExec(t, p, holder, "BEGIN")
		activityExec(t, p, holder, "LOCK TABLE activity_smoke IN ACCESS EXCLUSIVE MODE")
		for _, terminate := range []bool{false, true} {
			activityExec(t, p, peer, "BEGIN")
			wait, cancel := start()
			done := false
			func() {
				defer func() {
					cancel()
					if !done {
						wait()
					}
				}()
				statActivityBlocker(t, p, o, pid, hpid)
				statActivityAwait(t, p, o, pid, "relation")
				statActivitySignal(t, p, o, pid, terminate)
				out := wait()
				done = true
				if terminate {
					if out.Err == nil {
						t.Fatal("terminated waiter succeeded")
					}
				} else {
					fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
					r := statActivityGet(t, p, o, pid, true)
					statActivityIdleWait(t, b.PG, r)
					activityExec(t, p, peer, "ROLLBACK")
				}
			}()
		}
		statActivityRetired(t, p, o, pid)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_activity SET application_name='x' WHERE FALSE", statActivityMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		activityExec(t, p, holder, "ROLLBACK")
	})
}
