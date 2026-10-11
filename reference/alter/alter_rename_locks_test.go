//go:build reference

package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterRenameLock(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
		t.Run(q, func(t *testing.T) {
			p := fixture.NewPair(t)
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			a, b := fixture.LockConnections(t, ctx, p.Postgres)
			fixture.LockExec(t, ctx, a, "CREATE TABLE t(a INT)", 0)
			var pid int
			if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			s, err := b.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("utility Prepare waited/resolved", err)
			}
			defer s.Close()
			done := make(chan fixture.RelationOutcome, 1)
			go func() { r, err := s.ExecContext(ctx); done <- fixture.RelationOutcome{Result: r, Err: err} }()
			fixture.SchemaShadowWait(t, ctx, pid, "t")
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			out := fixture.LockResult(t, done)
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			fixture.LockAffected(t, out.Result, 0)
		})
	}
}
