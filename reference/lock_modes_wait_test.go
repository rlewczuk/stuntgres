package reference_test

import (
	"context"
	"testing"
)

func lockModesOrderWait(t *testing.T, pair *referencePair, backend referenceBackend) {
	t.Helper()
	ctx := pair.ctx
	a, b := referenceLockConnections(t, ctx, backend.db)
	c, err := backend.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	lockModesExec(t, ctx, a, "CREATE TABLE first(v INT)")
	lockModesExec(t, ctx, a, "CREATE TABLE later(v INT)")
	ap, bp := referenceLockPID(t, ctx, a), referenceLockPID(t, ctx, b)
	oid := referenceLockOID(t, ctx, "first")
	lockModesExec(t, ctx, a, "BEGIN")
	lockModesExec(t, ctx, b, "BEGIN")
	t.Cleanup(func() { a.ExecContext(ctx, "ROLLBACK"); b.ExecContext(ctx, "ROLLBACK") })
	lockModesExec(t, ctx, a, "LOCK first")
	await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
		r, err := b.ExecContext(ctx, "LOCK first, later, missing IN SHARE MODE")
		return relationOutcome{result: r, err: err}
	})
	referenceLockWaitBehind(t, ctx, bp, ap, oid, "ShareLock")
	// The exact pending resource, not elapsed time, proves first-target ordering;
	// a third connection independently proves later has not been acquired.
	lockModesProbe(t, ctx, c, "LOCK later NOWAIT", "", "")
	lockModesExec(t, ctx, a, "ROLLBACK")
	outcome := await()
	lockModesDiagnostic(t, outcome.err, "42P01", `relation "missing" does not exist`, "")
	lockModesExec(t, ctx, b, "ROLLBACK")
}

func lockModesSavepointWake(t *testing.T, pair *referencePair, backend referenceBackend) {
	t.Helper()
	ctx := pair.ctx
	a, b := referenceLockConnections(t, ctx, backend.db)
	lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
	ap, bp := referenceLockPID(t, ctx, a), referenceLockPID(t, ctx, b)
	oid := referenceLockOID(t, ctx, "t")
	lockModesExec(t, ctx, a, "BEGIN")
	lockModesExec(t, ctx, b, "BEGIN")
	t.Cleanup(func() { a.ExecContext(ctx, "ROLLBACK"); b.ExecContext(ctx, "ROLLBACK") })
	lockModesExec(t, ctx, a, "SAVEPOINT s")
	lockModesExec(t, ctx, a, "LOCK t")
	await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
		r, err := b.ExecContext(ctx, "LOCK t IN ROW EXCLUSIVE MODE")
		return relationOutcome{result: r, err: err}
	})
	referenceLockWaitBehind(t, ctx, bp, ap, oid, "RowExclusiveLock")
	lockModesExec(t, ctx, a, "ROLLBACK TO s")
	outcome := await()
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	referenceLockAffected(t, outcome.result, 0)
	// The original outer transaction is still active, not ended/replaced.
	lockModesExec(t, ctx, a, "SAVEPOINT still_active")
	lockModesExec(t, ctx, a, "RELEASE still_active")
	lockModesExec(t, ctx, b, "ROLLBACK")
	lockModesExec(t, ctx, a, "ROLLBACK")
}

func TestReferenceLockModesOrderWait(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		if !backend.pg {
			continue // Actual pending-resource observation requires pg_locks.
		}
		t.Run(backend.name, func(t *testing.T) { lockModesOrderWait(t, pair, backend) })
	}
}

func TestReferenceLockModesSavepointWake(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		if !backend.pg {
			continue // Actual waiter observation requires pg_locks.
		}
		t.Run(backend.name, func(t *testing.T) { lockModesSavepointWake(t, pair, backend) })
	}
}
