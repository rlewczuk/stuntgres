package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeSequenceOwnedTableLockSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						run := func(x fixture.Executor, step fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, step) }
						run(a, fixture.Exec("CREATE TABLE t(c INT)", 0))
						q := "CREATE SEQUENCE s START 23 OWNED BY t.c"
						if alter {
							run(b, fixture.Exec("CREATE SEQUENCE s START 23", 0))
							q = "ALTER SEQUENCE s OWNED BY t.c"
						}
						execute := ownedTableReferenceExec(t, p.Ctx, b, q, prepared)
						var ap, bp int
						var oid int64
						if backend.PG {
							ap, bp = fixture.SequenceOwnedLockPID(t, p.Ctx, a), fixture.SequenceOwnedLockPID(t, p.Ctx, b)
							oid = ownedTableReferenceOID(t, p.Ctx, a, "t")
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						run(holder, fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0))
						run(b, fixture.Exec("SET lock_timeout='100ms'", 0))
						await := fixture.LockOperation(t, p.Ctx, execute)
						if backend.PG {
							ownedTableReferenceQueue(t, p.Ctx, bp, ap, oid, "AccessShareLock")
						}
						o := await()
						if o.Result != nil {
							t.Fatal("failed ownership returned result")
						}
						want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						fixture.AssertDiagnostic(t, o.Err, &want)
						run(b, fixture.Exec("RESET lock_timeout", 0))
						// Native caller deadlines are ordinary context failures and preserve
						// the held session/handle. The wire adapter's cancellation is not
						// that contract; the shared 55P03 wait above retains its owner.
						if !backend.PG {
							bounded, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
							await = fixture.LockOperation(t, bounded, execute)
							o = await()
							cancel()
							var state interface{ SQLState() string }
							if o.Result != nil || !errors.Is(o.Err, context.DeadlineExceeded) || errors.As(o.Err, &state) {
								t.Fatal("ordinary dependency-lock cancellation", o.Result, o.Err)
							}
						}
						if err = holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(a, fixture.Exec("DROP TABLE t", 0))
						if alter {
							run(b, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(23)}}))
						} else {
							ownedTableReferenceMissing(t, p.Ctx, b, "s")
						}
						run(a, fixture.Exec("CREATE TABLE t(c INT)", 0))
						o = execute(p.Ctx)
						if o.Err != nil {
							t.Fatal("same retained ownership recovery", o.Err)
						}
						fixture.LockAffected(t, o.Result, 0)
						run(a, fixture.Exec("ALTER TABLE t DROP COLUMN c", 0))
						ownedTableReferenceMissing(t, p.Ctx, b, "s")
						run(b, fixture.Exec("CREATE SEQUENCE healthy START 41", 0))
						run(b, fixture.Select("SELECT nextval('healthy')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(41)}}))
					})
				}
			})
		}
	}
}
