package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockLifetimeSavepoints(t *testing.T) {
	for _, original := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference_key_share", true: "original_prepared_update"}[original], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					ap, bp := 0, 0
					if backend.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
					}
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					if original {
						stmt, err := tx.PrepareContext(p.Ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=11 WHERE id=1", 1)
						rows, err := stmt.QueryContext(p.Ctx)
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, backend.PG, 11)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", false), backend.PG, 10)
					}
					fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT s", 0)
					fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT v FROM t WHERE id=2 FOR SHARE", !original), backend.PG, 20)
					fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO s", 0)
					next := "UPDATE t SET v=21 WHERE id=2"
					conflict := "DELETE FROM t WHERE id=1"
					if original {
						next = "UPDATE t SET v=30 WHERE id=2"
						conflict = "UPDATE t SET v=30 WHERE id=1"
					}
					fixture.LockExec(t, p.Ctx, b, next, 1)
					var await func() fixture.RelationOutcome
					if backend.PG {
						await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, err := b.ExecContext(ctx, conflict)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RowWait(t, p.Ctx, bp, ap)
					} else {
						bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
						r, err := b.ExecContext(bounded, conflict)
						cancel()
						if r != nil || !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("premark grant lost at close/rollback-to", r, err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("caller deadline fabricated SQLSTATE", err)
						}
					}
					if original {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if backend.PG {
						o := await()
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						fixture.LockAffected(t, o.Result, 1)
					}
					if original {
						fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=30", 2)
					} else {
						if backend.PG {
							fixture.LockExec(t, p.Ctx, b, "INSERT INTO t VALUES(1,10)", 1)
						}
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t ORDER BY id", false), backend.PG, 10, 21)
					}
				})
			}
		})
	}
}
