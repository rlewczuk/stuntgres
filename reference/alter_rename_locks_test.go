//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceAlterRenameLock(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		t.Run(q, func(t *testing.T) {
			p := newReferencePair(t)
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			a, b := referenceLockConnections(t, ctx, p.postgres)
			referenceLockExec(t, ctx, a, "CREATE TABLE t(a INT)", 0)
			var pid int
			if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			s, err := b.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("utility Prepare waited/resolved", err)
			}
			defer s.Close()
			done := make(chan relationOutcome, 1)
			go func() { r, err := s.ExecContext(ctx); done <- relationOutcome{result: r, err: err} }()
			schemaShadowWait(t, ctx, pid, "t")
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			out := referenceLockResult(t, done)
			if out.err != nil {
				t.Fatal(out.err)
			}
			referenceLockAffected(t, out.result, 0)
		})
	}
}
