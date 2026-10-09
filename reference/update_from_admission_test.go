package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
)

func TestReferenceUpdateFromAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "prepare"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				holder, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				updateFromRun(t, p, b, holder)(referenceExec("ALTER TABLE s ALTER COLUMN n SET NOT NULL", 0))
				pid := 0
				if b.pg {
					pid = referenceLockPID(t, p.ctx, c)
				}
				q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k"
				join := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					var o relationOutcome
					if prepared {
						o.stmt, o.err = c.PrepareContext(ctx, q)
					} else {
						o.result, o.err = c.ExecContext(ctx, q)
					}
					return o
				})
				if b.pg {
					referenceLockWait(t, p.ctx, pid)
					var targetFirst bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND mode='RowExclusiveLock' AND granted)", pid).Scan(&targetFirst); err != nil || !targetFirst {
						t.Fatal("target not admitted before source", targetFirst, err)
					}
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				o := join()
				if o.err != nil {
					t.Fatal(o.err)
				}
				if prepared {
					defer o.stmt.Close()
					run(updateFromTargets(updateFromOriginal()))
					result, err := o.stmt.ExecContext(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					referenceLockAffected(t, result, 2)
				} else {
					referenceLockAffected(t, o.result, 2)
				}
			})
		})
	}
}

func TestReferenceUpdateFromPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"update_from_reader"})
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		updateFromSeed(run)
		run(referenceExec("CREATE ROLE update_from_reader", 0))
		if !b.pg {
			// SQL GRANT is excluded; positive physical grant coverage belongs to U4.
			_, err := c.ExecContext(p.ctx, "GRANT UPDATE(v) ON t TO update_from_reader")
			if err == nil {
				t.Fatal("native GRANT boundary unexpectedly admitted")
			}
			return
		}
		run(referenceExec("GRANT USAGE ON SCHEMA public TO update_from_reader", 0))
		run(referenceExec("GRANT UPDATE(v) ON t TO update_from_reader", 0))
		run(referenceExec("SET ROLE update_from_reader", 0))
		defer c.ExecContext(p.ctx, "RESET ROLE")
		want := controlsReferenceDiagnostic("42501", "permission denied for table s")
		updateFromDiagnostic(t, p.ctx, c, "UPDATE t SET v=1 FROM s WHERE FALSE", want, false)
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("GRANT SELECT(k,n) ON s TO update_from_reader", 0))
		run(referenceExec("SET ROLE update_from_reader", 0))
		run(referenceExec("UPDATE t SET v=1 FROM s WHERE FALSE", 0))
		updateFromDiagnostic(t, p.ctx, c, "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", controlsReferenceDiagnostic("42501", "permission denied for table t"), false)
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("GRANT SELECT(id) ON t TO update_from_reader", 0))
		run(referenceExec("SET ROLE update_from_reader", 0))
		run(referenceExec("UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", 2))
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("REVOKE SELECT(n) ON s FROM update_from_reader", 0))
		run(referenceExec("SET ROLE update_from_reader", 0))
		updateFromDiagnostic(t, p.ctx, c, "UPDATE t x SET v=1 FROM s WHERE x.id=s.k RETURNING s.n", want, false)
		run(referenceExec("RESET ROLE", 0))
	})
}

func TestReferenceUpdateFromCancellation(t *testing.T) {
	for _, phase := range []string{"before", "source_admission", "target_row"} {
		t.Run(phase, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				// Pre-execution context veto is a native control phase. lib/pq can
				// execute or retire a pre-canceled borrowed connection; neither incidental
				// race is a PostgreSQL SQL contract. PG cancellation uses observed waits.
				if phase == "before" && b.pg {
					return
				}
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				ctx, cancel := context.WithCancel(p.ctx)
				defer cancel()
				q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k"
				if phase == "before" {
					cancel()
					_, err := c.ExecContext(ctx, q)
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					run(updateFromTargets(updateFromOriginal()))
					return
				}
				holder, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				hold := updateFromRun(t, p, b, holder)
				if phase == "source_admission" {
					hold(referenceExec("ALTER TABLE s ALTER COLUMN n SET NOT NULL", 0))
				} else {
					hold(referenceExec("UPDATE t SET v=11 WHERE id=1", 1))
				}
				pid := 0
				if b.pg {
					pid = referenceLockPID(t, p.ctx, c)
				}
				join := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
					result, err := c.ExecContext(ctx, q)
					return relationOutcome{result: result, err: err}
				})
				if b.pg {
					updateFromPGWait(t, p.ctx, pid)
				}
				cancel()
				o := join()
				if o.err == nil {
					t.Fatal("canceled command succeeded")
				}
				if !b.pg && !errors.Is(o.err, context.Canceled) {
					t.Fatal(o.err)
				}
				if b.pg {
					want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
					if phase == "target_row" {
						diagnostic := referenceExtractDiagnostic(t, o.err)
						// Physical tuple identity is not portable reference data.
						if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(diagnostic.Context) {
							t.Fatal("tuple cancellation context", diagnostic.Context)
						}
						want.Context = diagnostic.Context
					}
					referenceAssertDiagnostic(t, o.err, &want)
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				// A new session proves no effects and can reacquire both source/target.
				peer := updateFromRun(t, p, b, b.db)
				peer(updateFromTargets(updateFromOriginal()))
				peer(referenceExec(q, 2))
			})
		})
	}
}
