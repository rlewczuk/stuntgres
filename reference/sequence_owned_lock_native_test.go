package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceSequenceOwnedTableLockNativeRetry(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, timeout := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Context", true: "/LockTimeout"}[timeout], func(t *testing.T) {
					p := newReferenceScenario(t)
					s := newTempReferenceSession(t, p, p.memory, p.memoryRecord, prepared)
					s.exec(s.c, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s START 17 OWNED BY t.c"
					if alter {
						s.exec(s.peer, "CREATE SEQUENCE s START 17", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					execute := ownedTableReferenceExec(t, p.ctx, s.peer, q, prepared)
					holder, err := s.c.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					s.exec(holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
					defer cancel()
					if timeout {
						s.exec(s.peer, "SET lock_timeout='100ms'", 0)
						ctx = p.ctx
					}
					o := execute(ctx)
					cancel()
					if o.result != nil {
						t.Fatal("failed ownership returned result")
					}
					if timeout {
						referenceAssertDiagnostic(t, o.err, tempReferenceDiagnostic("55P03", "canceling statement due to lock timeout", "", ""))
						s.exec(s.peer, "RESET lock_timeout", 0)
					} else {
						var diagnostic *stuntgres.Error
						if !errors.Is(o.err, context.DeadlineExceeded) || errors.As(o.err, &diagnostic) {
							t.Fatal("ordinary context failure", o.err)
						}
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Failed ALTER must not attach AUTO; failed CREATE must not publish a name.
					s.exec(s.c, "DROP TABLE t", 0)
					if alter {
						s.value(s.peer, "SELECT nextval('s')", 17)
					} else {
						s.missing(s.peer, "s", "last_value", "24")
					}
					// The very same prepared handle must revalidate a replaced dependency table.
					s.exec(s.c, "CREATE TABLE t(other INT)", 0)
					o = execute(p.ctx)
					if o.result != nil {
						t.Fatal("missing-column failure returned result")
					}
					referenceAssertDiagnostic(t, o.err, tempReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`, "", ""))
					s.exec(s.c, "DROP TABLE t", 0)
					s.exec(s.c, "CREATE TABLE t(c INT)", 0)
					o = execute(p.ctx)
					if o.err != nil {
						t.Fatal("healthy retained execution", o.err)
					}
					referenceLockAffected(t, o.result, 0)
					s.exec(s.c, "ALTER TABLE t DROP COLUMN c", 0)
					s.missing(s.peer, "s", "last_value", "24")
				})
			}
		}
	}
}
