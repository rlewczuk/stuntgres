package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Called by the durable smoke as a real feature phase, never another test alias.
func stateLockWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, o, peer *sql.Conn, id any, table string, whileBlocked func([]any)) {
	t.Helper()
	holder := statActivityConn(t, p, b)
	pid, hpid := statActivityPID(t, p, peer), statActivityPID(t, p, holder)
	activityExec(t, p, holder, "BEGIN")
	defer holder.ExecContext(context.Background(), "ROLLBACK")
	activityExec(t, p, holder, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE")
	q := "LOCK TABLE " + table + " IN ACCESS EXCLUSIVE MODE"
	ctx, cancel := context.WithCancel(p.Ctx)
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		_, err := peer.ExecContext(ctx, q)
		return fixture.RelationOutcome{Err: err}
	})
	joined := false
	defer func() {
		cancel()
		if !joined {
			await()
		}
	}()
	statActivityBlocker(t, p, o, pid, hpid)
	statActivityAwait(t, p, o, pid, "relation")
	if n := backendCount(t, p, o, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted AND locktype='relation' AND relation=$2::regclass AND mode='AccessExclusiveLock'", pid, table); n != 1 {
		t.Fatal("actual pending relation owner", n)
	}
	if n := backendCount(t, p, o, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND locktype='relation' AND relation=$2::regclass AND mode='AccessExclusiveLock'", hpid, table); n != 1 {
		t.Fatal("actual holder grant", n)
	}
	activityExec(t, p, o, "BEGIN")
	defer o.ExecContext(context.Background(), "ROLLBACK")
	held := statePrepare(t, p, o)
	row := stateRead(t, p, b, held, id)
	stateEqual(t, row[3:5], []any{"relation", "Lock"})
	if row[0] != q {
		t.Fatal("real waiting source", row)
	}
	if whileBlocked != nil {
		whileBlocked(row)
	}
	statActivitySignal(t, p, o, pid, false)
	out := await()
	joined = true
	fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	if backendCount(t, p, o, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid) != 0 {
		t.Fatal("canceled queue remains")
	}
	var blockers string
	if err := o.QueryRowContext(p.Ctx, "SELECT pg_blocking_pids($1)::text", pid).Scan(&blockers); err != nil || blockers != "{}" {
		t.Fatal(blockers, err)
	}
	if backendCount(t, p, o, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND relation=$2::regclass AND mode='AccessExclusiveLock'", hpid, table) != 1 {
		t.Fatal("cancel lost holder")
	}
	after := stateRead(t, p, b, held, id)
	stateEqual(t, after[:3], row[:3])
	stateIdle(t, b.PG, after)
	activityExec(t, p, o, "COMMIT")
}

func TestReferenceBackendStateWaits(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		mapped := stateMapped(t, p, b, o, peer)
		id, pid := mapped[0], mapped[1].(int64)
		activityExec(t, p, o, "CREATE TABLE bs_wait(v integer)")
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		stateLockWitness(t, p, b, o, peer, id, "bs_wait", nil)
		activityExec(t, p, peer, "ROLLBACK")
		statActivityClear(t, p, o)
		stateIdle(t, b.PG, stateRead(t, p, b, o, id))
		ctx, cancel := context.WithCancel(p.Ctx)
		await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			_, err := peer.ExecContext(ctx, "SELECT pg_sleep(600)")
			return fixture.RelationOutcome{Err: err}
		})
		joined := false
		defer func() {
			cancel()
			if !joined {
				await()
			}
		}()
		statActivityAwait(t, p, o, pid, "PgSleep")
		activityExec(t, p, o, "BEGIN")
		old := stateRead(t, p, b, o, id)
		stateEqual(t, old[3:5], []any{"PgSleep", "Timeout"})
		statActivitySignal(t, p, o, pid, false)
		out := await()
		joined = true
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		stateIdle(t, b.PG, stateRead(t, p, b, o, id))
		admin := statActivityConn(t, p, b)
		statActivitySignal(t, p, admin, pid, true)
		statActivityRetired(t, p, admin, pid)
		retired := stateRead(t, p, b, o, id)
		stateEqual(t, retired[:3], old[:3])
		stateEqual(t, retired[3:5], []any{nil, nil})
		activityExec(t, p, o, "COMMIT")
		stateAbsent(t, p, b, o, -1)
	})
}
