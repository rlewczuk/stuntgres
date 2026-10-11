package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func mergeUpdateOperation(ctx context.Context, worker *sql.Conn, held *sql.Stmt, q string) fixture.RelationOutcome {
	var r sql.Result
	var err error
	if held != nil {
		r, err = held.ExecContext(ctx)
	} else {
		r, err = worker.ExecContext(ctx, q)
	}
	return fixture.RelationOutcome{Result: r, Err: err}
}

// Native has no public pending-queue observer. A real SQL timeout proves
// blocking and atomic failure; the subsequent execution is explicitly a retry.
// RR recovers only the failed subtransaction, keeping its original snapshot.
func mergeUpdateNativeTimeout(t *testing.T, ctx context.Context, worker *sql.Conn, held *sql.Stmt, q string, repeatable bool) {
	t.Helper()
	if repeatable {
		fixture.LockExec(t, ctx, worker, "SAVEPOINT merge_wait", 0)
	}
	fixture.LockExec(t, ctx, worker, "SET lock_timeout='50ms'", 0)
	out := mergeUpdateOperation(ctx, worker, held, q)
	if out.Result != nil {
		t.Fatal("lock timeout returned result")
	}
	d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
	fixture.AssertDiagnostic(t, out.Err, &d)
	if repeatable {
		fixture.LockExec(t, ctx, worker, "ROLLBACK TO merge_wait", 0)
	}
	fixture.LockExec(t, ctx, worker, "SET lock_timeout=0", 0)
	if repeatable {
		fixture.LockExec(t, ctx, worker, "RELEASE merge_wait", 0)
	}
}
