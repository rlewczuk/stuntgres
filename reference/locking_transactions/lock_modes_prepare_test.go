package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func lockModesPrepare(t *testing.T, ctx context.Context, conn *sql.Conn, query string) *sql.Stmt {
	t.Helper()
	stmt, err := conn.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal("metadata preparation", query, err)
	}
	t.Cleanup(func() { stmt.Close() })
	return stmt
}

func TestReferenceLockModesPrepareLifecycle(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, b, "BEGIN")
		lockModesExec(t, ctx, b, "LOCK t")
		lockModesExec(t, ctx, a, "BEGIN")
		// Even the blocking form prepares while a live peer holds AX.
		blocking := lockModesPrepare(t, ctx, a, "LOCK t IN SHARE MODE")
		nowait := lockModesPrepare(t, ctx, a, "LOCK t IN SHARE MODE NOWAIT")
		_, err := nowait.ExecContext(ctx)
		lockModesDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`, "")
		_, err = a.ExecContext(ctx, "LOCK missing")
		lockModesDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
		lockModesExec(t, ctx, a, "ROLLBACK")
		lockModesExec(t, ctx, b, "ROLLBACK")
		lockModesExec(t, ctx, a, "BEGIN")
		result, err := blocking.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		lockModesExec(t, ctx, a, "COMMIT")
		_, err = blocking.ExecContext(ctx)
		lockModesDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")

		lockModesExec(t, ctx, a, "BEGIN")
		exclusive := lockModesPrepare(t, ctx, a, "LOCK t")
		// Successful live AS acquisition proves Prepare did not take AX.
		lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "", "")
		result, err = exclusive.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesExec(t, ctx, a, "ROLLBACK")
		_, err = exclusive.ExecContext(ctx)
		lockModesDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")

		// Idle metadata preparation neither resolves names nor caches absence.
		fresh := lockModesPrepare(t, ctx, a, "LOCK created_later")
		lockModesExec(t, ctx, a, "CREATE TABLE created_later(v INT)")
		lockModesExec(t, ctx, a, "BEGIN")
		result, err = fresh.ExecContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		lockModesProbe(t, ctx, b, "LOCK created_later IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "created_later"`)
		lockModesExec(t, ctx, a, "ROLLBACK")
	})
}
