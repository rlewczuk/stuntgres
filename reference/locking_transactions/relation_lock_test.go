package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRelationLockBlocking(t *testing.T) {
	for _, phase := range []string{"direct-select", "direct-insert", "prepare-select", "prepare-insert", "cached-select", "cached-insert"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, commit), func(t *testing.T) {
				pair := fixture.NewScenario(t)
				for _, backend := range pair.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := pair.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						selectQuery := phase == "direct-select" || phase == "prepare-select" || phase == "cached-select"
						q := "INSERT INTO t VALUES(2)"
						if selectQuery {
							q = "SELECT v FROM t ORDER BY v"
						}
						var cached *sql.Stmt
						var err error
						if phase == "cached-select" || phase == "cached-insert" {
							cached, err = b.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer cached.Close()
						}
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						utility, err := tx.PrepareContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
						if err != nil {
							t.Fatal("utility metadata prepare", err)
						}
						r, err := utility.ExecContext(ctx)
						utility.Close()
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockAffected(t, r, 0)
						fixture.LockExec(t, ctx, tx, "INSERT INTO t VALUES(1)", 1)
						pid := 0
						if backend.PG {
							if err = b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						} else {
							// Public native cancellation is a behavioral witness, not queue introspection.
							bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
							_, err = b.ExecContext(bounded, q)
							cancel()
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal("held AX must prevent native completion", err)
							}
						}
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							o := fixture.RelationOutcome{}
							switch phase {
							case "prepare-select", "prepare-insert":
								o.Stmt, o.Err = b.PrepareContext(ctx, q)
							case "cached-select":
								o.Rows, o.Err = cached.QueryContext(ctx)
							case "cached-insert":
								o.Result, o.Err = cached.ExecContext(ctx)
							default:
								if selectQuery {
									o.Rows, o.Err = b.QueryContext(ctx, q)
								} else {
									o.Result, o.Err = b.ExecContext(ctx, q)
								}
							}
							return o
						})
						if backend.PG {
							fixture.LockWaitBehind(t, ctx, pid, fixture.LockPID(t, ctx, a), fixture.LockOID(t, ctx, "t"), map[bool]string{false: "RowExclusiveLock", true: "AccessShareLock"}[selectQuery])
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := await()
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						if o.Stmt != nil {
							defer o.Stmt.Close()
							if selectQuery {
								o.Rows, o.Err = o.Stmt.QueryContext(ctx)
							} else {
								o.Result, o.Err = o.Stmt.ExecContext(ctx)
							}
							if o.Err != nil {
								t.Fatal(o.Err)
							}
						}
						if selectQuery {
							if commit {
								fixture.LimitLockValues(t, o, backend.PG, 1, 7)
							} else {
								fixture.LimitLockValues(t, o, backend.PG, 7)
							}
						} else {
							fixture.LockAffected(t, o.Result, 1)
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						if selectQuery {
							if commit {
								fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 7)
							} else {
								fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
							}
						} else {
							if commit {
								fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 2, 7)
							} else {
								fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 2, 7)
							}
						}
					})
				}
			})
		}
	}
}
func TestReferenceRelationLockCompatibilityAndPreparation(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(1)"} {
		for _, prepare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", q, prepare), func(t *testing.T) {
				pair := fixture.NewScenario(t)
				for _, backend := range pair.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := pair.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						implicit, err := a.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						implicit.Close()
						owner, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						owner.Rollback()
						reader, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Rollback()
						if prepare {
							stmt, err := reader.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							stmt.Close()
						} else {
							if q[0] == 'S' {
								rows, err := reader.QueryContext(ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
							} else {
								fixture.LockExec(t, ctx, reader, q, 1)
							}
						}
						// Compatible SELECT/INSERT completes before reader end, without private data.
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
						fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(8)", 1)
						writer, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Rollback()
						pid := 0
						if backend.PG {
							if err = writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						if !backend.PG {
							bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
							_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal("explicit prepare/statement lock not retained", err)
							}
							writer.Rollback()
							reader.Rollback()
						} else {
							await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
								r, err := writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
								return fixture.RelationOutcome{Result: r, Err: err}
							})
							fixture.LockWaitBehind(t, ctx, pid, fixture.LockPID(t, ctx, a), fixture.LockOID(t, ctx, "t"), "AccessExclusiveLock")
							reader.Rollback()
							o := await()
							if o.Err != nil {
								t.Fatal(o.Err)
							}
							fixture.LockAffected(t, o.Result, 0)
							writer.Rollback()
						}
						// Same-owner AX followed by automatic modes is self-compatible.
						owner, err = a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						fixture.LockExec(t, ctx, owner, "INSERT INTO t VALUES(9)", 1)
						rows, err = owner.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7, 8, 9)
						owner.Rollback()
					})
				}
			})
		}
	}
}
func TestReferenceRelationLockCancellation(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		for _, prepare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", q, prepare), func(t *testing.T) {
				pair := fixture.NewScenario(t)
				for _, backend := range pair.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := pair.Ctx
						a, b := fixture.LockConnections(t, ctx, backend.DB)
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						holder, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.LockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						waiter, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						// Cancellation must follow real lock admission; a deadline started
						// before analysis can expire without aborting the transaction.
						fixture.RelationLockActiveCancel(t, pair, backend, b, q, func(callCtx context.Context) fixture.RelationOutcome {
							o := fixture.RelationOutcome{}
							if prepare {
								o.Stmt, o.Err = waiter.PrepareContext(callCtx, q)
							} else {
								o.Result, o.Err = waiter.ExecContext(callCtx, q)
							}
							if o.Stmt != nil {
								o.Stmt.Close()
								t.Error("canceled Prepare exposed stmt")
							}
							return o
						})
						_, err = waiter.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
						if got := fixture.ExtractDiagnostic(t, err); got != fixture.TransactionAborted() {
							t.Fatal(got)
						}
						waiter.Rollback()
						holder.Rollback()
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
						fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(3)", 1)
					})
				}
			})
		}
	}
}
