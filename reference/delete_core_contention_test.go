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

	"stuntgres"
)

// Row DELETE waits on the holder transaction ID (not a relation AX lock).
// Both the ungranted lock and exact blocker are independently observed.
func deleteReferenceWaitRow(t *testing.T, ctx context.Context, waiter, holder int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&waiting)
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

// Capture the oracle's physical identity before any deletion. Native RowIDs
// are not PostgreSQL ctids and must never supply this diagnostic context.
func deleteReferenceTupleContext(t *testing.T, ctx context.Context, conn *sql.Conn) string {
	t.Helper()
	var tuple string
	if err := conn.QueryRowContext(ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("while deleting tuple %s in relation \"t\"", tuple)
}

func TestReferenceDeleteCoreContention(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("same_row/commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				for i, db := range []*sql.DB{p.memory, p.postgres} {
					t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
						r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
						a, b := referenceLockConnections(t, p.ctx, db)
						referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
						referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = b.PrepareContext(p.ctx, "DELETE FROM t WHERE id=$1")
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { stmt.Close() })
						}
						ap, bp := 0, 0
						if i == 1 {
							ap = referenceLockPID(t, p.ctx, a)
							bp = referenceLockPID(t, p.ctx, b)
						}
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=1", 1)
						// These commands synchronously finish before the holder ends: row2
						// and ordinary SELECT are independent of its row1 hold.
						referenceLockExec(t, p.ctx, b, "DELETE FROM t WHERE id=2", 1)
						deleteReferenceRows(t, p, b, r, 1)
						await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
							if stmt != nil {
								result, err := stmt.ExecContext(ctx, 1)
								return relationOutcome{result: result, err: err}
							}
							result, err := b.ExecContext(ctx, "DELETE FROM t WHERE id=1")
							return relationOutcome{result: result, err: err}
						})
						if i == 1 {
							deleteReferenceWaitRow(t, p.ctx, bp, ap)
						}
						// Native public results are not themselves queue proof; exact native
						// queue/candidate capture is independently covered by internal tests.
						if commit {
							err = holder.Commit()
						} else {
							err = holder.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := await()
						if o.err != nil {
							t.Fatal(o.err)
						}
						want := int64(1)
						if commit {
							want = 0
						}
						referenceLockAffected(t, o.result, want)
						deleteReferenceRows(t, p, b, r)
					})
				}
			})
		}
	}
	t.Run("postgres_fixed_candidates", func(t *testing.T) {
		p := newReferencePair(t)
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		c, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
		ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=1", 1)
		stmt, err := b.PrepareContext(p.ctx, "DELETE FROM t WHERE id>0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stmt.Close() })
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			result, err := stmt.ExecContext(ctx)
			return relationOutcome{result: result, err: err}
		})
		deleteReferenceWaitRow(t, p.ctx, bp, ap)
		referenceLockExec(t, p.ctx, c, "INSERT INTO t VALUES(2)", 1)
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := await()
		if o.err != nil {
			t.Fatal(o.err)
		}
		referenceLockAffected(t, o.result, 1)
		deleteReferenceRows(t, p, b, p.postgresRecord, 2)
		result, err := stmt.ExecContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceLockAffected(t, result, 1)
		deleteReferenceRows(t, p, b, p.postgresRecord)
	})
	for _, phase := range []string{"direct", "prepare", "cached"} {
		t.Run("relation/"+phase, func(t *testing.T) {
			p := newReferencePair(t)
			for i, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
					var cached *sql.Stmt
					if phase == "cached" {
						var err error
						cached, err = b.PrepareContext(p.ctx, "DELETE FROM t")
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { cached.Close() })
					}
					ap, bp := 0, 0
					if i == 1 {
						ap = referenceLockPID(t, p.ctx, a)
						bp = referenceLockPID(t, p.ctx, b)
					}
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						if phase == "prepare" {
							stmt, err := b.PrepareContext(ctx, "DELETE FROM t")
							return relationOutcome{stmt: stmt, err: err}
						}
						if cached != nil {
							result, err := cached.ExecContext(ctx)
							return relationOutcome{result: result, err: err}
						}
						result, err := b.ExecContext(ctx, "DELETE FROM t")
						return relationOutcome{result: result, err: err}
					})
					if i == 1 {
						referenceLockWaitBehind(t, p.ctx, bp, ap, "RowExclusiveLock")
					}
					if err = holder.Commit(); err != nil {
						t.Fatal(err)
					}
					o := await()
					if o.err != nil {
						t.Fatal(o.err)
					}
					if phase == "prepare" {
						if o.stmt == nil {
							t.Fatal("missing prepared handle")
						}
						defer o.stmt.Close()
						deleteReferenceRows(t, p, b, []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i], 1)
						result, err := o.stmt.ExecContext(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						referenceLockAffected(t, result, 1)
					} else {
						referenceLockAffected(t, o.result, 1)
					}
					deleteReferenceRows(t, p, b, []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i])
				})
			}
		})
	}
}

func TestReferenceDeleteCoreCancellation(t *testing.T) {
	t.Run("native_context_boundary", func(t *testing.T) {
		p := newReferencePair(t)
		a, b := referenceLockConnections(t, p.ctx, p.memory)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		result, err := b.ExecContext(ctx, "DELETE FROM t")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		deleteReferenceRows(t, p, b, p.memoryRecord, 1, 2)
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
		waiter, err := b.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { waiter.Rollback() })
		referenceLockExec(t, p.ctx, waiter, "INSERT INTO t VALUES(7)", 1)
		// id1 is encountered before held id2. A deadline terminates the real
		// operation, without sleep or an unobservable queue-state claim.
		ctx, cancel = context.WithTimeout(p.ctx, 150*time.Millisecond)
		defer cancel()
		result, err = waiter.ExecContext(ctx, "DELETE FROM t")
		if result != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(result, err)
		}
		var native *stuntgres.Error
		if errors.As(err, &native) {
			t.Fatal("context acquired SQLSTATE", err)
		}
		_, err = waiter.ExecContext(p.ctx, "SELECT id FROM t")
		referenceWaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if err = waiter.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		deleteReferenceRows(t, p, b, p.memoryRecord, 1, 2)
		referenceLockExec(t, p.ctx, b, "DELETE FROM t WHERE id=1", 1)
		deleteReferenceRows(t, p, b, p.memoryRecord, 2)
	})
	t.Run("postgres_cancel_backend", func(t *testing.T) {
		p := newReferencePair(t)
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
		ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
		tupleContext := deleteReferenceTupleContext(t, p.ctx, a)
		holder, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
		waiter, err := b.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { waiter.Rollback() })
		referenceLockExec(t, p.ctx, waiter, "INSERT INTO t VALUES(7)", 1)
		await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
			result, err := waiter.ExecContext(ctx, "DELETE FROM t")
			return relationOutcome{result: result, err: err}
		})
		deleteReferenceWaitRow(t, p.ctx, bp, ap)
		var canceled bool
		if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		o := await()
		if o.result != nil {
			t.Fatal("canceled DELETE returned result")
		}
		referenceAssertDiagnostic(t, o.err, &referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: "57014", Message: "canceling statement due to user request", Context: tupleContext,
		})
		_, err = waiter.ExecContext(p.ctx, "SELECT id FROM t")
		referenceWaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if err = waiter.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		deleteReferenceRows(t, p, b, p.postgresRecord, 1, 2)
		referenceLockExec(t, p.ctx, b, "DELETE FROM t WHERE id=1", 1)
		deleteReferenceRows(t, p, b, p.postgresRecord, 2)
	})
	for _, tc := range []struct{ setting, code, message string }{
		{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
		{"statement_timeout", "57014", "canceling statement due to statement timeout"},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			p := newReferencePair(t)
			for i, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
					ap, bp := 0, 0
					tupleContext := ""
					if i == 1 {
						ap = referenceLockPID(t, p.ctx, a)
						bp = referenceLockPID(t, p.ctx, b)
						tupleContext = deleteReferenceTupleContext(t, p.ctx, a)
					}
					referenceLockExec(t, p.ctx, b, "SET "+tc.setting+"='300ms'", 0)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceLockExec(t, p.ctx, holder, "DELETE FROM t WHERE id=2", 1)
					stmt, err := b.PrepareContext(p.ctx, "DELETE FROM t")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { stmt.Close() })
					await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						result, err := stmt.ExecContext(ctx)
						return relationOutcome{result: result, err: err}
					})
					if i == 1 {
						deleteReferenceWaitRow(t, p.ctx, bp, ap)
					}
					o := await()
					if o.result != nil {
						t.Fatal("timeout returned result")
					}
					referenceAssertDiagnostic(t, o.err, &referenceDiagnostic{
						Severity: "ERROR", SeverityUnlocalized: "ERROR",
						Code: tc.code, Message: tc.message, Context: tupleContext,
					})
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceLockExec(t, p.ctx, b, "RESET "+tc.setting, 0)
					deleteReferenceRows(t, p, b, []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i], 1, 2)
					referenceLockExec(t, p.ctx, b, "DELETE FROM t", 2)
				})
			}
		})
	}
}

func TestReferenceDeleteCorePrepareLocks(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, db)
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
			owner, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Rollback()
			stmt, err := owner.PrepareContext(p.ctx, "DELETE FROM t WHERE id=$1")
			if err != nil {
				t.Fatal(err)
			}
			// Preparation has RX but neither selects candidates nor locks row1.
			referenceLockExec(t, p.ctx, b, "DELETE FROM t WHERE id=1", 1)
			if err = stmt.Close(); err != nil {
				t.Fatal(err)
			}
			deleteReferenceRows(t, p, owner, r)
			challenger, err := b.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer challenger.Rollback()
			_, err = challenger.ExecContext(p.ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
			referenceWaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
			if err = challenger.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = owner.Commit(); err != nil {
				t.Fatal(err)
			}
			free, err := b.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer free.Rollback()
			referenceLockExec(t, p.ctx, free, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			if err = free.Commit(); err != nil {
				t.Fatal(err)
			}
			referenceLockExec(t, p.ctx, b, "INSERT INTO t VALUES(2)", 1)
			deleteReferenceRows(t, p, b, r, 2)
		})
	}
}
