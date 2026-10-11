package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func lockModesOrderWait(t *testing.T, pair *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	ctx := pair.Ctx
	a, b := fixture.LockConnections(t, ctx, backend.DB)
	c, err := backend.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	lockModesExec(t, ctx, a, "CREATE TABLE first(v INT)")
	lockModesExec(t, ctx, a, "CREATE TABLE later(v INT)")
	ap, bp := fixture.LockPID(t, ctx, a), fixture.LockPID(t, ctx, b)
	oid := fixture.LockOID(t, ctx, "first")
	lockModesExec(t, ctx, a, "BEGIN")
	lockModesExec(t, ctx, b, "BEGIN")
	t.Cleanup(func() { a.ExecContext(ctx, "ROLLBACK"); b.ExecContext(ctx, "ROLLBACK") })
	lockModesExec(t, ctx, a, "LOCK first")
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		r, err := b.ExecContext(ctx, "LOCK first, later, missing IN SHARE MODE")
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	fixture.LockWaitBehind(t, ctx, bp, ap, oid, "ShareLock")
	// The exact pending resource, not elapsed time, proves first-target ordering;
	// a third connection independently proves later has not been acquired.
	lockModesProbe(t, ctx, c, "LOCK later NOWAIT", "", "")
	lockModesExec(t, ctx, a, "ROLLBACK")
	outcome := await()
	lockModesDiagnostic(t, outcome.Err, "42P01", `relation "missing" does not exist`, "")
	lockModesExec(t, ctx, b, "ROLLBACK")
}

func lockModesSavepointWake(t *testing.T, pair *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	ctx := pair.Ctx
	a, b := fixture.LockConnections(t, ctx, backend.DB)
	lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
	ap, bp := fixture.LockPID(t, ctx, a), fixture.LockPID(t, ctx, b)
	oid := fixture.LockOID(t, ctx, "t")
	lockModesExec(t, ctx, a, "BEGIN")
	lockModesExec(t, ctx, b, "BEGIN")
	t.Cleanup(func() { a.ExecContext(ctx, "ROLLBACK"); b.ExecContext(ctx, "ROLLBACK") })
	lockModesExec(t, ctx, a, "SAVEPOINT s")
	lockModesExec(t, ctx, a, "LOCK t")
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		r, err := b.ExecContext(ctx, "LOCK t IN ROW EXCLUSIVE MODE")
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	fixture.LockWaitBehind(t, ctx, bp, ap, oid, "RowExclusiveLock")
	lockModesExec(t, ctx, a, "ROLLBACK TO s")
	outcome := await()
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	fixture.LockAffected(t, outcome.Result, 0)
	// The original outer transaction is still active, not ended/replaced.
	lockModesExec(t, ctx, a, "SAVEPOINT still_active")
	lockModesExec(t, ctx, a, "RELEASE still_active")
	lockModesExec(t, ctx, b, "ROLLBACK")
	lockModesExec(t, ctx, a, "ROLLBACK")
}

func TestReferenceLockModesOrderWait(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		if !backend.PG {
			continue // Actual pending-resource observation requires pg_locks.
		}
		t.Run(backend.Name, func(t *testing.T) { lockModesOrderWait(t, pair, backend) })
	}
}

func TestReferenceLockModesSavepointWake(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		if !backend.PG {
			continue // Actual waiter observation requires pg_locks.
		}
		t.Run(backend.Name, func(t *testing.T) { lockModesSavepointWake(t, pair, backend) })
	}
}
