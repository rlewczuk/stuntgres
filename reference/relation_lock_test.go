package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceRelationLockBlocking(t *testing.T) {
	for _, phase := range []string{"direct-select", "direct-insert", "prepare-select", "prepare-insert", "cached-select", "cached-insert"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, commit), func(t *testing.T) {
				pair := newReferenceScenario(t)
				for _, backend := range pair.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, backend.db)
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
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
						referenceLockAffected(t, r, 0)
						referenceLockExec(t, ctx, tx, "INSERT INTO t VALUES(1)", 1)
						pid := 0
						if backend.pg {
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
						await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
							o := relationOutcome{}
							switch phase {
							case "prepare-select", "prepare-insert":
								o.stmt, o.err = b.PrepareContext(ctx, q)
							case "cached-select":
								o.rows, o.err = cached.QueryContext(ctx)
							case "cached-insert":
								o.result, o.err = cached.ExecContext(ctx)
							default:
								if selectQuery {
									o.rows, o.err = b.QueryContext(ctx, q)
								} else {
									o.result, o.err = b.ExecContext(ctx, q)
								}
							}
							return o
						})
						if backend.pg {
							referenceLockWaitBehind(t, ctx, pid, referenceLockPID(t, ctx, a), referenceLockOID(t, ctx, "t"), map[bool]string{false: "RowExclusiveLock", true: "AccessShareLock"}[selectQuery])
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
						if o.err != nil {
							t.Fatal(o.err)
						}
						if o.stmt != nil {
							defer o.stmt.Close()
							if selectQuery {
								o.rows, o.err = o.stmt.QueryContext(ctx)
							} else {
								o.result, o.err = o.stmt.ExecContext(ctx)
							}
							if o.err != nil {
								t.Fatal(o.err)
							}
						}
						if selectQuery {
							if commit {
								referenceLimitLockValues(t, o, backend.pg, 1, 7)
							} else {
								referenceLimitLockValues(t, o, backend.pg, 7)
							}
						} else {
							referenceLockAffected(t, o.result, 1)
						}
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						if selectQuery {
							if commit {
								referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 1, 7)
							} else {
								referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7)
							}
						} else {
							if commit {
								referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 1, 2, 7)
							} else {
								referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 2, 7)
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
				pair := newReferenceScenario(t)
				for _, backend := range pair.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, backend.db)
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						implicit, err := a.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						implicit.Close()
						owner, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						referenceLockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
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
								referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7)
							} else {
								referenceLockExec(t, ctx, reader, q, 1)
							}
						}
						// Compatible SELECT/INSERT completes before reader end, without private data.
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(8)", 1)
						writer, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Rollback()
						pid := 0
						if backend.pg {
							if err = writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						if !backend.pg {
							bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
							_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal("explicit prepare/statement lock not retained", err)
							}
							writer.Rollback()
							reader.Rollback()
						} else {
							await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
								r, err := writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
								return relationOutcome{result: r, err: err}
							})
							referenceLockWaitBehind(t, ctx, pid, referenceLockPID(t, ctx, a), referenceLockOID(t, ctx, "t"), "AccessExclusiveLock")
							reader.Rollback()
							o := await()
							if o.err != nil {
								t.Fatal(o.err)
							}
							referenceLockAffected(t, o.result, 0)
							writer.Rollback()
						}
						// Same-owner AX followed by automatic modes is self-compatible.
						owner, err = a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						referenceLockExec(t, ctx, owner, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						referenceLockExec(t, ctx, owner, "INSERT INTO t VALUES(9)", 1)
						rows, err = owner.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7, 8, 9)
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
				pair := newReferenceScenario(t)
				for _, backend := range pair.backends() {
					t.Run(backend.name, func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, backend.db)
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
						holder, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceLockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
						waiter, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						pid := 0
						if backend.pg {
							if err = waiter.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						callCtx, cancel := context.WithCancel(ctx)
						if !backend.pg {
							cancel()
							callCtx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
						}
						defer cancel()
						await := referenceLockOperation(t, callCtx, func(ctx context.Context) relationOutcome {
							o := relationOutcome{}
							if prepare {
								o.stmt, o.err = waiter.PrepareContext(ctx, q)
							} else {
								o.result, o.err = waiter.ExecContext(ctx, q)
							}
							return o
						})
						if backend.pg {
							referenceLockWaitBehind(t, ctx, pid, referenceLockPID(t, ctx, a), referenceLockOID(t, ctx, "t"), map[bool]string{false: "RowExclusiveLock", true: "AccessShareLock"}[q[0] == 'S'])
							var canceled bool
							if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
								t.Fatal(canceled, err)
							}
						}
						o := await()
						if o.stmt != nil {
							o.stmt.Close()
							t.Fatal("canceled Prepare exposed stmt")
						}
						if !backend.pg {
							if !errors.Is(o.err, context.DeadlineExceeded) {
								t.Fatal(o.err)
							}
							var state interface{ SQLState() string }
							if errors.As(o.err, &state) {
								t.Fatal("ordinary caller deadline fabricated SQLSTATE", o.err)
							}
						} else {
							want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
							if got := referenceExtractDiagnostic(t, o.err); got != want {
								t.Fatal(got, want)
							}
						}
						_, err = waiter.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
						if got := referenceExtractDiagnostic(t, err); got != referenceTransactionAborted() {
							t.Fatal(got)
						}
						waiter.Rollback()
						holder.Rollback()
						rows, err := b.QueryContext(ctx, "SELECT v FROM t")
						if err != nil {
							t.Fatal(err)
						}
						referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(3)", 1)
					})
				}
			})
		}
	}
}
