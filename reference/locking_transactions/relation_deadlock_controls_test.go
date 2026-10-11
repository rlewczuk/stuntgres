package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationDeadlockDiagnosticsNegative(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			// Commit setup so later victim rollback cannot remove the relation.
			fixture.LockExec(t, ctx, a, "COMMIT", 0)
			for _, tc := range []struct{ q, code, message, pos string }{
				{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", ""},
				{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
				{"SELECT * FROM t WHERE v = )", "42601", `syntax error at or near ")"`, "27"},
			} {
				_, err := a.ExecContext(ctx, tc.q)
				fixture.WaitsDiagnostic(t, err, tc.code, tc.message, tc.pos)
			}
			fixture.LockExec(t, ctx, a, "BEGIN", 0)
			fixture.LockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			fixture.LockExec(t, ctx, b, "BEGIN", 0)
			_, err := b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
			fixture.WaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
			deadlockPublicAborted(t, ctx, b, "SELECT v FROM t")
			fixture.LockExec(t, ctx, b, "ROLLBACK", 0)
			fixture.LockExec(t, ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, ctx, a, "CREATE ROLE deadlock_oracle_reader", 0)
			if backend.PG {
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					a.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "RESET SESSION AUTHORIZATION")
					if _, err := fixture.Admin.ExecContext(cleanup, "DROP OWNED BY deadlock_oracle_reader; DROP ROLE deadlock_oracle_reader"); err != nil {
						t.Error(err)
					}
				})
				// public is recreated without PUBLIC schema USAGE by the fixture.
				if _, err := fixture.Admin.ExecContext(ctx, "GRANT USAGE ON SCHEMA public TO deadlock_oracle_reader"); err != nil {
					t.Fatal(err)
				}
			}
			fixture.LockExec(t, ctx, b, "SET SESSION AUTHORIZATION deadlock_oracle_reader", 0)
			fixture.LockExec(t, ctx, b, "BEGIN", 0)
			_, err = b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			fixture.WaitsDiagnostic(t, err, "42501", "permission denied for table t")
			deadlockPublicAborted(t, ctx, b, "SELECT v FROM t")
			fixture.LockExec(t, ctx, b, "ROLLBACK", 0)
			fixture.LockExec(t, ctx, b, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, ctx, a, "BEGIN", 0)
			fixture.LockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			fixture.LockExec(t, ctx, a, "ROLLBACK", 0)
		})
	}
}
