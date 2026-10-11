package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateFromAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "prepare"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				holder, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				updateFromRun(t, p, b, holder)(fixture.Exec("ALTER TABLE s ALTER COLUMN n SET NOT NULL", 0))
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k"
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					var o fixture.RelationOutcome
					if prepared {
						o.Stmt, o.Err = c.PrepareContext(ctx, q)
					} else {
						o.Result, o.Err = c.ExecContext(ctx, q)
					}
					return o
				})
				if b.PG {
					fixture.LockWait(t, p.Ctx, pid)
					var targetFirst bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND mode='RowExclusiveLock' AND granted)", pid).Scan(&targetFirst); err != nil || !targetFirst {
						t.Fatal("target not admitted before source", targetFirst, err)
					}
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				o := join()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				if prepared {
					defer o.Stmt.Close()
					run(updateFromTargets(updateFromOriginal()))
					result, err := o.Stmt.ExecContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.LockAffected(t, result, 2)
				} else {
					fixture.LockAffected(t, o.Result, 2)
				}
			})
		})
	}
}

func TestReferenceUpdateFromPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"update_from_reader"})
	updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		updateFromSeed(run)
		run(fixture.Exec("CREATE ROLE update_from_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO update_from_reader", 0))
		}
		run(fixture.Exec("GRANT UPDATE(v) ON t TO update_from_reader", 0))
		run(fixture.Exec("SET ROLE update_from_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table s")
		updateFromDiagnostic(t, p.Ctx, c, "UPDATE t SET v=1 FROM s WHERE FALSE", want, false)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT(k,n) ON s TO update_from_reader", 0))
		run(fixture.Exec("SET ROLE update_from_reader", 0))
		run(fixture.Exec("UPDATE t SET v=1 FROM s WHERE FALSE", 0))
		updateFromDiagnostic(t, p.Ctx, c, "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"), false)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT(id) ON t TO update_from_reader", 0))
		run(fixture.Exec("SET ROLE update_from_reader", 0))
		run(fixture.Exec("UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", 2))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE SELECT(n) ON s FROM update_from_reader", 0))
		run(fixture.Exec("SET ROLE update_from_reader", 0))
		updateFromDiagnostic(t, p.Ctx, c, "UPDATE t x SET v=1 FROM s WHERE x.id=s.k RETURNING s.n", want, false)
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceUpdateFromCancellation(t *testing.T) {
	for _, phase := range []string{"before", "source_admission", "target_row"} {
		t.Run(phase, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Pre-execution context veto is a native control phase. lib/pq can
				// execute or retire a pre-canceled borrowed connection; neither incidental
				// race is a PostgreSQL SQL contract. PG cancellation uses observed waits.
				if phase == "before" && b.PG {
					return
				}
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				ctx, cancel := context.WithCancel(p.Ctx)
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
				holder, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				hold := updateFromRun(t, p, b, holder)
				if phase == "source_admission" {
					hold(fixture.Exec("ALTER TABLE s ALTER COLUMN n SET NOT NULL", 0))
				} else {
					hold(fixture.Exec("UPDATE t SET v=11 WHERE id=1", 1))
				}
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					result, err := c.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				if b.PG {
					updateFromPGWait(t, p.Ctx, pid)
				}
				cancel()
				o := join()
				if o.Err == nil {
					t.Fatal("canceled command succeeded")
				}
				if !b.PG && !errors.Is(o.Err, context.Canceled) {
					t.Fatal(o.Err)
				}
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					if phase == "target_row" {
						diagnostic := fixture.ExtractDiagnostic(t, o.Err)
						// Physical tuple identity is not portable reference data.
						if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(diagnostic.Context) {
							t.Fatal("tuple cancellation context", diagnostic.Context)
						}
						want.Context = diagnostic.Context
					}
					fixture.AssertDiagnostic(t, o.Err, &want)
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				// A new session proves no effects and can reacquire both source/target.
				peer := updateFromRun(t, p, b, b.DB)
				peer(updateFromTargets(updateFromOriginal()))
				peer(fixture.Exec(q, 2))
			})
		})
	}
}
