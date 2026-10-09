package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceRelationDeadlockDiagnosticsNegative(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			a, b := referenceLockConnections(t, ctx, backend.db)
			referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			// Commit setup so later victim rollback cannot remove the relation.
			referenceLockExec(t, ctx, a, "COMMIT", 0)
			for _, tc := range []struct{ q, code, message, pos string }{
				{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", ""},
				{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
				{"SELECT * FROM t WHERE v = )", "42601", `syntax error at or near ")"`, "27"},
			} {
				_, err := a.ExecContext(ctx, tc.q)
				referenceWaitsDiagnostic(t, err, tc.code, tc.message, tc.pos)
			}
			referenceLockExec(t, ctx, a, "BEGIN", 0)
			referenceLockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			referenceLockExec(t, ctx, b, "BEGIN", 0)
			_, err := b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
			referenceWaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
			deadlockPublicAborted(t, ctx, b, "SELECT v FROM t")
			referenceLockExec(t, ctx, b, "ROLLBACK", 0)
			referenceLockExec(t, ctx, a, "ROLLBACK", 0)
			referenceLockExec(t, ctx, a, "CREATE ROLE deadlock_oracle_reader", 0)
			if backend.pg {
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					a.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "RESET SESSION AUTHORIZATION")
					if _, err := referenceAdmin.ExecContext(cleanup, "DROP OWNED BY deadlock_oracle_reader; DROP ROLE deadlock_oracle_reader"); err != nil {
						t.Error(err)
					}
				})
				// public is recreated without PUBLIC schema USAGE by the fixture.
				if _, err := referenceAdmin.ExecContext(ctx, "GRANT USAGE ON SCHEMA public TO deadlock_oracle_reader"); err != nil {
					t.Fatal(err)
				}
			}
			referenceLockExec(t, ctx, b, "SET SESSION AUTHORIZATION deadlock_oracle_reader", 0)
			referenceLockExec(t, ctx, b, "BEGIN", 0)
			_, err = b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			referenceWaitsDiagnostic(t, err, "42501", "permission denied for table t")
			deadlockPublicAborted(t, ctx, b, "SELECT v FROM t")
			referenceLockExec(t, ctx, b, "ROLLBACK", 0)
			referenceLockExec(t, ctx, b, "RESET SESSION AUTHORIZATION", 0)
			referenceLockExec(t, ctx, a, "BEGIN", 0)
			referenceLockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			referenceLockExec(t, ctx, a, "ROLLBACK", 0)
		})
	}
}
