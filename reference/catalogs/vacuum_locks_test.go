package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				holder := vacuumConn(t, p, b)
				var notices []fixture.Diagnostic
				_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, rec, s) }
				run(fixture.Exec("CREATE TABLE vacuum_locks(id INT)", 0))
				run(fixture.Exec("INSERT INTO vacuum_locks VALUES(1),(2)", 2))
				run(fixture.Exec("DELETE FROM vacuum_locks WHERE id=1", 1))
				q := "VACUUM (TRUNCATE false) vacuum_locks"
				h := vacuumHeld(t, p, worker, q)
				for _, mode := range []string{"ACCESS SHARE", "ROW EXCLUSIVE"} {
					vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
					vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE vacuum_locks IN "+mode+" MODE", 0))
					if prepared {
						fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
					vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				}
				vacuumCount(t, p.Ctx, worker, "vacuum_locks", 2)
				vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE vacuum_locks IN SHARE MODE", 0))
				notices = nil
				s := fixture.Exec("VACUUM (SKIP_LOCKED) vacuum_locks", 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				vacuumWarning(t, notices, "55P03", `skipping vacuum of "vacuum_locks" --- lock not available`)
				vacuumCount(t, p.Ctx, worker, "vacuum_locks", 2)
				for _, tc := range []struct{ setting, code, message string }{
					{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
					{"statement_timeout", "57014", "canceling statement due to statement timeout"},
				} {
					run(fixture.Exec("SET "+tc.setting+"='100ms'", 0))
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					if prepared {
						fixture.RunStep(t, p.Ctx, h, rec, s)
					} else {
						run(s)
					}
					run(fixture.Exec("RESET "+tc.setting, 0))
				}
				// Existing shared helper observes the actual queued request, cancels,
				// joins, asserts full57014 on PG/context cancellation on native, and
				// retains this same physical worker for recovery checks.
				waiterPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, worker)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "vacuum_locks")
				}
				fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, q, "(*relationRequest).wait", func(ctx context.Context) {
					if b.PG {
						fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock")
						var progress int
						if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_progress_vacuum WHERE pid=$1", waiterPID).Scan(&progress); err != nil {
							t.Fatal(err)
						}
						if progress != 0 {
							t.Fatal("progress before work-lock grant", progress)
						}
					}
				}, func(ctx context.Context) fixture.RelationOutcome {
					var out fixture.RelationOutcome
					if prepared {
						out.Result, out.Err = h.Statement.ExecContext(ctx)
					} else {
						out.Result, out.Err = worker.ExecContext(ctx, q)
					}
					return out
				})
				vacuumCount(t, p.Ctx, worker, "vacuum_locks", 2)
				vacuumRows(t, p, b, worker, "vacuum_locks", 2)
				vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				if prepared {
					fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 0))
				}
				vacuumCount(t, p.Ctx, worker, "vacuum_locks", 3)
				vacuumProgressEmpty(t, p, b, worker)
			})
		})
	}
}
