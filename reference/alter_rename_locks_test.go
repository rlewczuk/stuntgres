//go:build reference

package reference_test

import (
	"context"
	"database/sql"
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

func TestReferenceAlterRenameLockTimeout(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.postgres, p.memory} {
		a, b := referenceLockConnections(t, p.ctx, db)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(a INT)", 0)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceLockExec(t, p.ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
		referenceLockExec(t, p.ctx, b, "SET lock_timeout='20ms'", 0)
		for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"})
			failure.prepared = true
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, b, &referenceRecorder{}, failure)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, b, &referenceRecorder{}, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{}))
	}
}
