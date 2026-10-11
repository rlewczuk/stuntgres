package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceOwnedTableLockNativeRetry(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, timeout := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Context", true: "/LockTimeout"}[timeout], func(t *testing.T) {
					p := fixture.NewScenario(t)
					s := fixture.NewTempReferenceSession(t, p, p.Memory, p.MemoryRecord, prepared)
					s.Exec(s.C, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s START 17 OWNED BY t.c"
					if alter {
						s.Exec(s.Peer, "CREATE SEQUENCE s START 17", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					execute := ownedTableReferenceExec(t, p.Ctx, s.Peer, q, prepared)
					holder, err := s.C.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					s.Exec(holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
					defer cancel()
					if timeout {
						s.Exec(s.Peer, "SET lock_timeout='100ms'", 0)
						ctx = p.Ctx
					}
					o := execute(ctx)
					cancel()
					if o.Result != nil {
						t.Fatal("failed ownership returned result")
					}
					if timeout {
						fixture.AssertDiagnostic(t, o.Err, fixture.TempReferenceDiagnostic("55P03", "canceling statement due to lock timeout", "", ""))
						s.Exec(s.Peer, "RESET lock_timeout", 0)
					} else {
						var diagnostic *stuntgres.Error
						if !errors.Is(o.Err, context.DeadlineExceeded) || errors.As(o.Err, &diagnostic) {
							t.Fatal("ordinary context failure", o.Err)
						}
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					// Failed ALTER must not attach AUTO; failed CREATE must not publish a name.
					s.Exec(s.C, "DROP TABLE t", 0)
					if alter {
						s.Value(s.Peer, "SELECT nextval('s')", 17)
					} else {
						s.Missing(s.Peer, "s", "last_value", "24")
					}
					// The very same prepared handle must revalidate a replaced dependency table.
					s.Exec(s.C, "CREATE TABLE t(other INT)", 0)
					o = execute(p.Ctx)
					if o.Result != nil {
						t.Fatal("missing-column failure returned result")
					}
					fixture.AssertDiagnostic(t, o.Err, fixture.TempReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`, "", ""))
					s.Exec(s.C, "DROP TABLE t", 0)
					s.Exec(s.C, "CREATE TABLE t(c INT)", 0)
					o = execute(p.Ctx)
					if o.Err != nil {
						t.Fatal("healthy retained execution", o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					s.Exec(s.C, "ALTER TABLE t DROP COLUMN c", 0)
					s.Missing(s.Peer, "s", "last_value", "24")
				})
			}
		}
	}
}
