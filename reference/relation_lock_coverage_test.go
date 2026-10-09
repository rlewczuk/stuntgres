//go:build reference

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
	pair := newReferencePair(t)
	for index, db := range []*sql.DB{pair.memory, pair.postgres} {
		t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
			ctx := pair.ctx
			a, b := referenceLockConnections(t, ctx, db)
			referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
			ap, bp := 0, 0
			if index == 1 {
				ap = referenceLockPID(t, ctx, a)
				bp = referenceLockPID(t, ctx, b)
			}
			// Begin at the driver seam so database/sql does not own a sql.Tx
			// that would serialize Raw eviction behind its transaction lifetime.
			if err := a.Raw(func(raw any) error {
				_, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			// On failure physically evict A before fixture reset as well.
			t.Cleanup(func() { _ = a.Raw(func(any) error { return driver.ErrBadConn }) })
			referenceLockExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
			await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
				rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				return relationOutcome{rows: rows, err: err}
			})
			if index == 1 {
				referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "public.t"), "AccessShareLock")
			}
			// ErrBadConn makes database/sql close the physical driver connection;
			// sql.Conn.Close alone would only return a borrower to the pool.
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal("physical eviction", err)
			}
			o := await()
			if o.err != nil {
				t.Fatal("waiter after physical end", o.err)
			}
			referenceTransactionValues(t, o.rows, 7)
			healthy, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { healthy.Rollback() })
			referenceLockExec(t, ctx, healthy, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			rows, err := healthy.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			referenceTransactionValues(t, rows, 7)
			if err := healthy.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceRelationLockExclusiveConflict(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/commit=%t", prepared, commit), func(t *testing.T) {
				pair := newReferencePair(t)
				for index, db := range []*sql.DB{pair.memory, pair.postgres} {
					t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
						ctx := pair.ctx
						a, b := referenceLockConnections(t, ctx, db)
						c, err := db.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { c.Close() })
						referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						ap, bp, cp := 0, 0, 0
						if index == 1 {
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
						if index == 1 {
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
						if index == 1 {
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
