package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceRelationLockContextBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
						run(a, referenceExec("CREATE TABLE t(v INT)", 0))
						cached, err := b.PrepareContext(p.ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						defer cached.Close()
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run(tx, referenceStep{query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared: prepared, affected: 0})
						run(tx, referenceStep{query: "INSERT INTO t VALUES(1)", prepared: prepared, affected: 1})
						if !backend.pg {
							for _, query := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
								bounded, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
								o := referenceRowExecute(bounded, b, query, prepared)
								cancel()
								if o.result != nil || !errors.Is(o.err, context.DeadlineExceeded) {
									t.Fatal(query, o.result, o.err)
								}
								var state interface{ SQLState() string }
								if errors.As(o.err, &state) {
									t.Fatal("caller deadline acquired SQLSTATE", o.err)
								}
							}
							bounded, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
							rows, err := cached.QueryContext(bounded)
							cancel()
							if rows != nil {
								rows.Close()
								t.Fatal("canceled cached query returned a cursor")
							}
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal(err)
							}
							var state interface{ SQLState() string }
							if errors.As(err, &state) {
								t.Fatal("cached caller deadline acquired SQLSTATE", err)
							}
						}
						var await func() relationOutcome
						if backend.pg {
							bp, ap := referenceLockPID(t, p.ctx, b), referenceLockPID(t, p.ctx, a)
							await = referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
								rows, err := cached.QueryContext(ctx)
								return relationOutcome{rows: rows, err: err}
							})
							referenceLockWaitBehind(t, p.ctx, bp, ap, referenceLockOID(t, p.ctx, "t"), "AccessShareLock")
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						want := []int64{}
						if commit {
							want = []int64{1}
						}
						if await != nil {
							referenceLimitLockValues(t, await(), true, want...)
						}
						rows, err := cached.QueryContext(p.ctx)
						referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, backend.pg, want...)
						run(b, referenceStep{query: "INSERT INTO t VALUES(2)", prepared: prepared, affected: 1})
						if commit {
							want = []int64{1, 2}
						} else {
							want = []int64{2}
						}
						run(b, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", want...))
					})
				}
			})
		}
	}
}
