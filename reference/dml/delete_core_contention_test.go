//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Row DELETE waits on the holder transaction ID (not a relation AX lock).
// Both the ungranted lock and exact blocker are independently observed.
func deleteReferenceWaitRow(t *testing.T, ctx context.Context, waiter, holder int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := fixture.Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&waiting)
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
				p := fixture.NewPair(t)
				for i, db := range []*sql.DB{p.Memory, p.Postgres} {
					t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
						r := []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i]
						a, b := fixture.LockConnections(t, p.Ctx, db)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = b.PrepareContext(p.Ctx, "DELETE FROM t WHERE id=$1")
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { stmt.Close() })
						}
						ap, bp := 0, 0
						if i == 1 {
							ap = fixture.LockPID(t, p.Ctx, a)
							bp = fixture.LockPID(t, p.Ctx, b)
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { holder.Rollback() })
						fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=1", 1)
						// These commands synchronously finish before the holder ends: row2
						// and ordinary SELECT are independent of its row1 hold.
						fixture.LockExec(t, p.Ctx, b, "DELETE FROM t WHERE id=2", 1)
						fixture.DeleteReferenceRows(t, p, b, r, 1)
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							if stmt != nil {
								result, err := stmt.ExecContext(ctx, 1)
								return fixture.RelationOutcome{Result: result, Err: err}
							}
							result, err := b.ExecContext(ctx, "DELETE FROM t WHERE id=1")
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						if i == 1 {
							deleteReferenceWaitRow(t, p.Ctx, bp, ap)
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
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						want := int64(1)
						if commit {
							want = 0
						}
						fixture.LockAffected(t, o.Result, want)
						fixture.DeleteReferenceRows(t, p, b, r)
					})
				}
			})
		}
	}
	t.Run("postgres_fixed_candidates", func(t *testing.T) {
		p := fixture.NewPair(t)
		a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
		c, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1)", 1)
		ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=1", 1)
		stmt, err := b.PrepareContext(p.Ctx, "DELETE FROM t WHERE id>0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stmt.Close() })
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			result, err := stmt.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		deleteReferenceWaitRow(t, p.Ctx, bp, ap)
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(2)", 1)
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := await()
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		fixture.LockAffected(t, o.Result, 1)
		fixture.DeleteReferenceRows(t, p, b, p.PostgresRecord, 2)
		result, err := stmt.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		fixture.DeleteReferenceRows(t, p, b, p.PostgresRecord)
	})
	for _, phase := range []string{"direct", "prepare", "cached"} {
		t.Run("relation/"+phase, func(t *testing.T) {
			p := fixture.NewPair(t)
			for i, db := range []*sql.DB{p.Memory, p.Postgres} {
				t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, db)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1)", 1)
					var cached *sql.Stmt
					if phase == "cached" {
						var err error
						cached, err = b.PrepareContext(p.Ctx, "DELETE FROM t")
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { cached.Close() })
					}
					ap, bp := 0, 0
					if i == 1 {
						ap = fixture.LockPID(t, p.Ctx, a)
						bp = fixture.LockPID(t, p.Ctx, b)
					}
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						if phase == "prepare" {
							stmt, err := b.PrepareContext(ctx, "DELETE FROM t")
							return fixture.RelationOutcome{Stmt: stmt, Err: err}
						}
						if cached != nil {
							result, err := cached.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						}
						result, err := b.ExecContext(ctx, "DELETE FROM t")
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					if i == 1 {
						fixture.LockWaitBehind(t, p.Ctx, bp, ap, fixture.LockOID(t, p.Ctx, "public.t"), "RowExclusiveLock")
					}
					if err = holder.Commit(); err != nil {
						t.Fatal(err)
					}
					o := await()
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					if phase == "prepare" {
						if o.Stmt == nil {
							t.Fatal("missing prepared handle")
						}
						defer o.Stmt.Close()
						fixture.DeleteReferenceRows(t, p, b, []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i], 1)
						result, err := o.Stmt.ExecContext(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockAffected(t, result, 1)
					} else {
						fixture.LockAffected(t, o.Result, 1)
					}
					fixture.DeleteReferenceRows(t, p, b, []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i])
				})
			}
		})
	}
}

func TestReferenceDeleteCoreCancellation(t *testing.T) {
	t.Run("native_context_boundary", func(t *testing.T) {
		p := fixture.NewPair(t)
		a, b := fixture.LockConnections(t, p.Ctx, p.Memory)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		result, err := b.ExecContext(ctx, "DELETE FROM t")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		fixture.DeleteReferenceRows(t, p, b, p.MemoryRecord, 1, 2)
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=2", 1)
		waiter, err := b.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { waiter.Rollback() })
		fixture.LockExec(t, p.Ctx, waiter, "INSERT INTO t VALUES(7)", 1)
		// id1 is encountered before held id2. A deadline terminates the real
		// operation, without sleep or an unobservable queue-state claim.
		ctx, cancel = context.WithTimeout(p.Ctx, 150*time.Millisecond)
		defer cancel()
		result, err = waiter.ExecContext(ctx, "DELETE FROM t")
		if result != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(result, err)
		}
		var native *stuntgres.Error
		if errors.As(err, &native) {
			t.Fatal("context acquired SQLSTATE", err)
		}
		_, err = waiter.ExecContext(p.Ctx, "SELECT id FROM t")
		fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if err = waiter.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceRows(t, p, b, p.MemoryRecord, 1, 2)
		fixture.LockExec(t, p.Ctx, b, "DELETE FROM t WHERE id=1", 1)
		fixture.DeleteReferenceRows(t, p, b, p.MemoryRecord, 2)
	})
	t.Run("postgres_cancel_backend", func(t *testing.T) {
		p := fixture.NewPair(t)
		a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
		ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, b)
		tupleContext := deleteReferenceTupleContext(t, p.Ctx, a)
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { holder.Rollback() })
		fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=2", 1)
		waiter, err := b.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { waiter.Rollback() })
		fixture.LockExec(t, p.Ctx, waiter, "INSERT INTO t VALUES(7)", 1)
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			result, err := waiter.ExecContext(ctx, "DELETE FROM t")
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		deleteReferenceWaitRow(t, p.Ctx, bp, ap)
		var canceled bool
		if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		o := await()
		if o.Result != nil {
			t.Fatal("canceled DELETE returned result")
		}
		fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: "57014", Message: "canceling statement due to user request", Context: tupleContext,
		})
		_, err = waiter.ExecContext(p.Ctx, "SELECT id FROM t")
		fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		if err = waiter.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceRows(t, p, b, p.PostgresRecord, 1, 2)
		fixture.LockExec(t, p.Ctx, b, "DELETE FROM t WHERE id=1", 1)
		fixture.DeleteReferenceRows(t, p, b, p.PostgresRecord, 2)
	})
	for _, tc := range []struct{ setting, code, message string }{
		{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
		{"statement_timeout", "57014", "canceling statement due to statement timeout"},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			p := fixture.NewPair(t)
			for i, db := range []*sql.DB{p.Memory, p.Postgres} {
				t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, db)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2)", 2)
					ap, bp := 0, 0
					tupleContext := ""
					if i == 1 {
						ap = fixture.LockPID(t, p.Ctx, a)
						bp = fixture.LockPID(t, p.Ctx, b)
						tupleContext = deleteReferenceTupleContext(t, p.Ctx, a)
					}
					fixture.LockExec(t, p.Ctx, b, "SET "+tc.setting+"='300ms'", 0)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.LockExec(t, p.Ctx, holder, "DELETE FROM t WHERE id=2", 1)
					stmt, err := b.PrepareContext(p.Ctx, "DELETE FROM t")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { stmt.Close() })
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						result, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					if i == 1 {
						deleteReferenceWaitRow(t, p.Ctx, bp, ap)
					}
					o := await()
					if o.Result != nil {
						t.Fatal("timeout returned result")
					}
					fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{
						Severity: "ERROR", SeverityUnlocalized: "ERROR",
						Code: tc.code, Message: tc.message, Context: tupleContext,
					})
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.LockExec(t, p.Ctx, b, "RESET "+tc.setting, 0)
					fixture.DeleteReferenceRows(t, p, b, []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i], 1, 2)
					fixture.LockExec(t, p.Ctx, b, "DELETE FROM t", 2)
				})
			}
		})
	}
}

func TestReferenceDeleteCorePrepareLocks(t *testing.T) {
	p := fixture.NewPair(t)
	for i, db := range []*sql.DB{p.Memory, p.Postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, db)
			r := []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i]
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1)", 1)
			owner, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Rollback()
			stmt, err := owner.PrepareContext(p.Ctx, "DELETE FROM t WHERE id=$1")
			if err != nil {
				t.Fatal(err)
			}
			// Preparation has RX but neither selects candidates nor locks row1.
			fixture.LockExec(t, p.Ctx, b, "DELETE FROM t WHERE id=1", 1)
			if err = stmt.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceRows(t, p, owner, r)
			challenger, err := b.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer challenger.Rollback()
			_, err = challenger.ExecContext(p.Ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT")
			fixture.WaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
			if err = challenger.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = owner.Commit(); err != nil {
				t.Fatal(err)
			}
			free, err := b.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer free.Rollback()
			fixture.LockExec(t, p.Ctx, free, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
			if err = free.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, b, "INSERT INTO t VALUES(2)", 1)
			fixture.DeleteReferenceRows(t, p, b, r, 2)
		})
	}
}
