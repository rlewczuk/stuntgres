//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"
)

type relationOutcome struct {
	rows   *sql.Rows
	stmt   *sql.Stmt
	result sql.Result
	err    error
}

func referenceLockWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}
func referenceLockResult(t *testing.T, done <-chan relationOutcome) relationOutcome {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("lock command did not finish")
		return relationOutcome{}
	}
}
func referenceLockAffected(t *testing.T, r sql.Result, want int64) {
	t.Helper()
	if r == nil {
		t.Fatal("missing result")
	}
	n, err := r.RowsAffected()
	if err != nil || n != want {
		t.Fatal(n, err, "want", want)
	}
}
func referenceLockConnections(t *testing.T, ctx context.Context, db *sql.DB) (*sql.Conn, *sql.Conn) {
	t.Helper()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return a, b
}
func referenceLockExec(t *testing.T, ctx context.Context, x referenceExecutor, q string, n int64) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	referenceLockAffected(t, r, n)
}
func TestReferenceRelationLockBlocking(t *testing.T) {
	for _, phase := range []string{"direct-select", "direct-insert", "prepare-select", "prepare-insert", "cached-select", "cached-insert"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, commit), func(t *testing.T) {
				pair := newReferencePair(t)
				for index, db := range []*sql.DB{pair.memory, pair.postgres} {
					t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, db)
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
						if index == 1 {
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
						done := make(chan relationOutcome, 1)
						go func() {
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
							done <- o
						}()
						if index == 1 {
							referenceLockWait(t, ctx, pid)
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := referenceLockResult(t, done)
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
								referenceTransactionValues(t, o.rows, 1, 7)
							} else {
								referenceTransactionValues(t, o.rows, 7)
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
								referenceTransactionValues(t, rows, 1, 7)
							} else {
								referenceTransactionValues(t, rows, 7)
							}
						} else {
							if commit {
								referenceTransactionValues(t, rows, 1, 2, 7)
							} else {
								referenceTransactionValues(t, rows, 2, 7)
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
				pair := newReferencePair(t)
				for index, db := range []*sql.DB{pair.memory, pair.postgres} {
					t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, db)
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
								referenceTransactionValues(t, rows, 7)
							} else {
								referenceLockExec(t, ctx, reader, q, 1)
							}
						}
						// Compatible SELECT/INSERT completes before reader end, without private data.
						rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						referenceTransactionValues(t, rows, 7)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(8)", 1)
						writer, err := b.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Rollback()
						pid := 0
						if index == 1 {
							if err = writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						if index == 0 {
							bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
							_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal("explicit prepare/statement lock not retained", err)
							}
							writer.Rollback()
							reader.Rollback()
						} else {
							done := make(chan relationOutcome, 1)
							go func() {
								r, err := writer.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
								done <- relationOutcome{result: r, err: err}
							}()
							referenceLockWait(t, ctx, pid)
							reader.Rollback()
							o := referenceLockResult(t, done)
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
						referenceTransactionValues(t, rows, 7, 8, 9)
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
				pair := newReferencePair(t)
				for index, db := range []*sql.DB{pair.memory, pair.postgres} {
					t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, db)
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
						if index == 1 {
							if err = waiter.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						callCtx, cancel := context.WithCancel(ctx)
						if index == 0 {
							cancel()
							callCtx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
						}
						defer cancel()
						done := make(chan relationOutcome, 1)
						go func() {
							o := relationOutcome{}
							if prepare {
								o.stmt, o.err = waiter.PrepareContext(callCtx, q)
							} else {
								o.result, o.err = waiter.ExecContext(callCtx, q)
							}
							done <- o
						}()
						if index == 1 {
							referenceLockWait(t, ctx, pid)
							var canceled bool
							if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
								t.Fatal(canceled, err)
							}
						}
						o := referenceLockResult(t, done)
						if o.stmt != nil {
							o.stmt.Close()
							t.Fatal("canceled Prepare exposed stmt")
						}
						if index == 0 {
							if !errors.Is(o.err, context.DeadlineExceeded) {
								t.Fatal(o.err)
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
						referenceTransactionValues(t, rows, 7)
						referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(3)", 1)
					})
				}
			})
		}
	}
}
