package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceRowLockLifetimeSavepoints(t *testing.T) {
	for _, original := range []bool{false, true} {
		t.Run(map[bool]string{false: "reference_key_share", true: "original_prepared_update"}[original], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					ap, bp := 0, 0
					if backend.pg {
						ap, bp = referenceRowPIDs(t, p.ctx, a, b)
					}
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					if original {
						stmt, err := tx.PrepareContext(p.ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						referenceLockExec(t, p.ctx, b, "UPDATE t SET v=11 WHERE id=1", 1)
						rows, err := stmt.QueryContext(p.ctx)
						referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, backend.pg, 11)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", false), backend.pg, 10)
					}
					referenceLockExec(t, p.ctx, tx, "SAVEPOINT s", 0)
					referenceLimitLockValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=2 FOR SHARE", !original), backend.pg, 20)
					referenceLockExec(t, p.ctx, tx, "ROLLBACK TO s", 0)
					next := "UPDATE t SET v=21 WHERE id=2"
					conflict := "DELETE FROM t WHERE id=1"
					if original {
						next = "UPDATE t SET v=30 WHERE id=2"
						conflict = "UPDATE t SET v=30 WHERE id=1"
					}
					referenceLockExec(t, p.ctx, b, next, 1)
					var await func() relationOutcome
					if backend.pg {
						await = referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
							r, err := b.ExecContext(ctx, conflict)
							return relationOutcome{result: r, err: err}
						})
						referenceRowWait(t, p.ctx, bp, ap)
					} else {
						bounded, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
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
					if backend.pg {
						o := await()
						if o.err != nil {
							t.Fatal(o.err)
						}
						referenceLockAffected(t, o.result, 1)
					}
					if original {
						referenceLockExec(t, p.ctx, b, "UPDATE t SET v=30", 2)
					} else {
						if backend.pg {
							referenceLockExec(t, p.ctx, b, "INSERT INTO t VALUES(1,10)", 1)
						}
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t ORDER BY id", false), backend.pg, 10, 21)
					}
				})
			}
		})
	}
}
