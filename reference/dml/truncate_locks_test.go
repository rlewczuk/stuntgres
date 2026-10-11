package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateLocks(t *testing.T) {
	for _, mode := range []string{"ACCESS SHARE", "ROW SHARE", "ROW EXCLUSIVE", "SHARE UPDATE EXCLUSIVE", "SHARE", "SHARE ROW EXCLUSIVE", "EXCLUSIVE", "ACCESS EXCLUSIVE"} {
		t.Run(mode, func(t *testing.T) {
			truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				truncateSeed(t, f)
				tx := truncateBegin(t, p, f.A, nil)
				truncateExec(t, f, tx, "LOCK TABLE q IN "+mode+" MODE", 0)
				truncateExec(t, f, f.B, "SET lock_timeout='30ms'", 0)
				// A full structured server timeout is a real conflict witness on native,
				// not a sleep-only assertion about an unobserved native queue.
				truncateError(t, f, f.B, "TRUNCATE p,q", truncateWant("55P03", "canceling statement due to lock timeout"))
				tx.Rollback()
				truncateRows(t, f, f.A, "p", 1, 2)
				truncateRows(t, f, f.A, "q", 7)
				truncateExec(t, f, f.B, "SET lock_timeout=0", 0)
				truncateExec(t, f, f.B, "TRUNCATE p,q", 0)
				truncateRows(t, f, f.A, "p")
				truncateRows(t, f, f.A, "q")
			})
		})
	}
	t.Run("StatementTimeout", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "LOCK q IN ACCESS SHARE MODE", 0)
			truncateExec(t, f, f.B, "SET statement_timeout='30ms'", 0)
			truncateError(t, f, f.B, "TRUNCATE p,q", truncateWant("57014", "canceling statement due to statement timeout"))
			truncateExec(t, f, f.B, "SET statement_timeout=0", 0)
			tx.Rollback()
			truncateRows(t, f, f.A, "p", 1, 2)
			truncateRows(t, f, f.A, "q", 7)
		})
	})
	t.Run("CancellationAndPrefix", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			var waiter, holder int
			var oid int64
			if b.PG {
				waiter = fixture.LockPID(t, p.Ctx, f.B)
				holder = fixture.LockPID(t, p.Ctx, f.A)
				oid = fixture.LockOID(t, p.Ctx, "q")
			}
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "LOCK q IN ACCESS SHARE MODE", 0)
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				r, e := f.B.ExecContext(ctx, "TRUNCATE p,q")
				return fixture.RelationOutcome{Result: r, Err: e}
			})
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessExclusiveLock")
				var prefix bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='p'::regclass AND mode='AccessExclusiveLock' AND granted)`, waiter).Scan(&prefix); err != nil || !prefix {
					t.Fatalf("earlier target not held: %v %v", prefix, err)
				}
				var canceled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&canceled); err != nil || !canceled {
					t.Fatalf("backend cancellation %v: %v", canceled, err)
				}
			} else {
				// No public native pg_locks provider. Exact pending resource/prefix proof is
				// internal; this independently covers the public caller-context identity.
				cancel()
			}
			o := join()
			if o.Result != nil {
				t.Fatal("canceled truncate returned result")
			}
			if b.PG {
				d := truncateWant("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, o.Err, &d)
			} else if !errors.Is(o.Err, context.Canceled) {
				t.Fatalf("caller cancellation identity: %v", o.Err)
			}
			tx.Rollback()
			truncateRows(t, f, f.A, "p", 1, 2)
			truncateRows(t, f, f.A, "q", 7)
			// A new caller acquires both: no canceled queue or prefix survives.
			truncateExec(t, f, f.A, "TRUNCATE p,q", 0)
		})
	})
}
func TestReferenceTruncateAdmission(t *testing.T) {
	t.Run("SerializedTruncateWriters", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			var waiter, holder int
			var oid int64
			if b.PG {
				waiter = fixture.LockPID(t, p.Ctx, f.B)
				holder = fixture.LockPID(t, p.Ctx, f.A)
				oid = fixture.LockOID(t, p.Ctx, "p")
			}
			first := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, first, "TRUNCATE p", 0)
			truncateExec(t, f, first, "INSERT INTO p VALUES(3)", 1)
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				result, err := f.B.ExecContext(ctx, "TRUNCATE p")
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessExclusiveLock")
			}
			// Native public API checks serial results, not an invented queue provider;
			// TestTruncateAdmission proves the actual native pending resource/lineage.
			if err := first.Commit(); err != nil {
				t.Fatal(err)
			}
			outcome := join()
			if outcome.Err != nil || outcome.Result == nil {
				t.Fatalf("second truncate: %v", outcome.Err)
			}
			if count, err := outcome.Result.RowsAffected(); err != nil || count != 0 {
				t.Fatalf("second truncate affected %d: %v", count, err)
			}
			truncateRows(t, f, f.A, "p")
			truncateExec(t, f, f.B, "INSERT INTO p VALUES(4)", 1)
			truncateRows(t, f, f.A, "p", 4)
		})
	})
	t.Run("RequestedTargetOrder", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "LOCK p IN ACCESS SHARE MODE", 0)
			truncateExec(t, f, f.B, "SET lock_timeout='30ms'", 0)
			truncateError(t, f, f.B, "TRUNCATE p,missing", truncateWant("55P03", "canceling statement due to lock timeout"))
			truncateError(t, f, f.B, "TRUNCATE missing,p", truncateWant("42P01", `relation "missing" does not exist`))
			tx.Rollback()
			truncateRows(t, f, f.A, "p", 1, 2)
		})
	})
	t.Run("GrantTimeACL", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			truncateExec(t, f, f.A, "CREATE ROLE truncate_waiter", 0)
			defer func() {
				f.B.ExecContext(p.Ctx, "ROLLBACK")
				f.B.ExecContext(p.Ctx, "RESET ROLE")
				f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_waiter")
				f.A.ExecContext(p.Ctx, "DROP ROLE truncate_waiter")
			}()
			if b.PG {
				truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO truncate_waiter", 0)
			}
			truncateExec(t, f, f.A, "GRANT TRUNCATE ON p TO truncate_waiter", 0)
			if !b.PG {
				// Public native has no exact queue introspection. The shared held-Prepare
				// reauthorization is deterministic; private units cover revoke after queue.
				stmt, err := f.B.PrepareContext(p.Ctx, "TRUNCATE p")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				truncateExec(t, f, f.A, "REVOKE TRUNCATE ON p FROM truncate_waiter", 0)
				truncateExec(t, f, f.B, "SET ROLE truncate_waiter", 0)
				_, err = stmt.ExecContext(p.Ctx)
				d := truncateWant("42501", "permission denied for table p")
				fixture.AssertDiagnostic(t, err, &d)
			} else {
				holder := fixture.LockPID(t, p.Ctx, f.A)
				waiter := fixture.LockPID(t, p.Ctx, f.B)
				oid := fixture.LockOID(t, p.Ctx, "p")
				tx := truncateBegin(t, p, f.A, nil)
				truncateExec(t, f, tx, "LOCK p IN ACCESS SHARE MODE", 0)
				truncateExec(t, f, f.B, "SET ROLE truncate_waiter", 0)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := f.B.ExecContext(ctx, "TRUNCATE p")
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessExclusiveLock")
				truncateExec(t, f, tx, "REVOKE TRUNCATE ON p FROM truncate_waiter", 0)
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				o := join()
				d := truncateWant("42501", "permission denied for table p")
				fixture.AssertDiagnostic(t, o.Err, &d)
				if o.Result != nil {
					t.Fatal("denied result")
				}
			}
			truncateRows(t, f, f.A, "p", 1, 2)
		})
	})
	t.Run("PGProviderLateDependency", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if !b.PG {
				return
			} // SQL CREATE TABLE in explicit tx is an absent native provider; internal graph units cover refresh.
			for _, hierarchy := range []bool{false, true} {
				truncateExec(t, f, f.A, "CREATE TABLE late_parent(id INT PRIMARY KEY)", 0)
				truncateExec(t, f, f.A, "INSERT INTO late_parent VALUES(1)", 1)
				holder := fixture.LockPID(t, p.Ctx, f.A)
				waiter := fixture.LockPID(t, p.Ctx, f.B)
				oid := fixture.LockOID(t, p.Ctx, "late_parent")
				tx := truncateBegin(t, p, f.A, nil)
				truncateExec(t, f, tx, "LOCK late_parent IN SHARE ROW EXCLUSIVE MODE", 0)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := f.B.ExecContext(ctx, "TRUNCATE late_parent CASCADE")
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessExclusiveLock")
				if hierarchy {
					truncateExec(t, f, tx, "CREATE TABLE late_child() INHERITS(late_parent)", 0)
					truncateExec(t, f, tx, "INSERT INTO late_child VALUES(2)", 1)
				} else {
					truncateExec(t, f, tx, "CREATE TABLE late_child(id INT REFERENCES late_parent)", 0)
					truncateExec(t, f, tx, "INSERT INTO late_child VALUES(1)", 1)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				o := join()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 0)
				if hierarchy {
					truncateNotices(t, f)
				} else {
					truncateNotices(t, f, "late_child")
				}
				truncateRows(t, f, f.A, "late_child")
				truncateExec(t, f, f.A, "DROP TABLE late_child", 0)
				truncateExec(t, f, f.A, "DROP TABLE late_parent", 0)
			}
		})
	})
}
