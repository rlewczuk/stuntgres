package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationLockPhysicalEnd(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "eviction", true: "driver_close"}[direct], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := pair.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					fixture.LockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(7)", 1)
					var raw driver.Conn
					var closeRaw func() error
					if direct {
						raw, closeRaw = referenceTransactionRaw(t, pair, backend)
					}
					ap, bp := 0, 0
					if backend.PG {
						bp = fixture.LockPID(t, ctx, b)
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
							ap = fixture.LockPID(t, ctx, a)
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
						fixture.LockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
					}
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "public.t"), "AccessShareLock")
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
					fixture.LimitLockValues(t, await(), backend.PG, 7)
					healthy, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer healthy.Rollback()
					fixture.LockExec(t, ctx, healthy, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					rows, err := healthy.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, backend.PG, 7)
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
				pair := fixture.NewScenario(t)
				for _, backend := range pair.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := pair.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						c, err := backend.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { c.Close() })
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						ap, bp, cp := 0, 0, 0
						if backend.PG {
							ap = fixture.LockPID(t, ctx, a)
							bp = fixture.LockPID(t, ctx, b)
							cp = fixture.LockPID(t, ctx, c)
						}
						owner, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { owner.Rollback() })
						fixture.LockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
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
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = waiter.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							}
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						if backend.PG {
							fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "public.t"), "AccessExclusiveLock")
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
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						fixture.LockAffected(t, o.Result, 0)
						if stmt != nil {
							stmt.Close()
						}
						successor, err := c.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { successor.Rollback() })
						awaitSuccessor := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, err := successor.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						if backend.PG {
							fixture.LockWaitBehind(t, ctx, cp, bp, fixture.LockOID(t, ctx, "public.t"), "AccessExclusiveLock")
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
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						fixture.LockAffected(t, o.Result, 0)
						if err := successor.Rollback(); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}
