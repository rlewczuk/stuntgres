package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceRelationLockPhysicalEnd(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "eviction", true: "driver_close"}[direct], func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := pair.ctx
					a, b := referenceLockConnections(t, ctx, backend.db)
					referenceLockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(7)", 1)
					var raw driver.Conn
					var closeRaw func() error
					if direct {
						raw, closeRaw = referenceTransactionRaw(t, pair, backend)
					}
					ap, bp := 0, 0
					if backend.pg {
						bp = referenceLockPID(t, ctx, b)
						if direct {
							rows, err := raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT pg_backend_pid()", nil)
							if err != nil {
								t.Fatal(err)
							}
							value := make([]driver.Value, 1)
							if err := rows.Next(value); err != nil {
								rows.Close()
								t.Fatal(err)
							}
							ap = int(value[0].(int64))
							if err := rows.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							ap = referenceLockPID(t, ctx, a)
						}
					}
					if direct {
						if _, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{}); err != nil {
							t.Fatal(err)
						}
						referenceTransactionRawExec(t, ctx, raw, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						referenceTransactionRawExec(t, ctx, raw, "INSERT INTO t VALUES(1)", 1)
					} else {
						if err := a.Raw(func(raw any) error { _, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{}); return err }); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = a.Raw(func(any) error { return driver.ErrBadConn }) })
						referenceLockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
					}
					await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						return relationOutcome{rows: rows, err: err}
					})
					if backend.pg {
						referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "public.t"), "AccessShareLock")
					}
					if direct {
						if err := closeRaw(); err != nil {
							t.Fatal("actual driver.Close", err)
						}
					} else {
						if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
							t.Fatal("physical eviction", err)
						}
					}
					referenceLimitLockValues(t, await(), backend.pg, 7)
					healthy, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer healthy.Rollback()
					referenceLockExec(t, ctx, healthy, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					rows, err := healthy.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, backend.pg, 7)
					if err := healthy.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestReferenceRelationLockExclusiveConflict(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/commit=%t", prepared, commit), func(t *testing.T) {
				pair := newReferenceScenario(t)
				for _, backend := range pair.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, backend.db)
						c, err := backend.db.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { c.Close() })
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						ap, bp, cp := 0, 0, 0
						if backend.pg {
							ap = referenceLockPID(t, ctx, a)
							bp = referenceLockPID(t, ctx, b)
							cp = referenceLockPID(t, ctx, c)
						}
						owner, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { owner.Rollback() })
						referenceLockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						waiter, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { waiter.Rollback() })
						var stmt *sql.Stmt
						if prepared {
							// Must finish while A still holds AX: Prepare is metadata-only.
							prepareCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
							stmt, err = waiter.PrepareContext(prepareCtx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if err != nil {
								t.Fatal("metadata-only Prepare behind AX", err)
							}
							t.Cleanup(func() { stmt.Close() })
						}
						await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = waiter.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							}
							return relationOutcome{result: result, err: err}
						})
						if backend.pg {
							referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "public.t"), "AccessExclusiveLock")
						}
						if commit {
							err = owner.Commit()
						} else {
							err = owner.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := await()
						if o.err != nil {
							t.Fatal(o.err)
						}
						referenceLockAffected(t, o.result, 0)
						if stmt != nil {
							stmt.Close()
						}
						successor, err := c.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { successor.Rollback() })
						awaitSuccessor := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							r, err := successor.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							return relationOutcome{result: r, err: err}
						})
						if backend.pg {
							referenceLockWaitBehind(t, ctx, cp, bp, referenceLockOID(t, ctx, "public.t"), "AccessExclusiveLock")
						}
						if commit {
							err = waiter.Commit()
						} else {
							err = waiter.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o = awaitSuccessor()
						if o.err != nil {
							t.Fatal(o.err)
						}
						referenceLockAffected(t, o.result, 0)
						if err := successor.Rollback(); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}
