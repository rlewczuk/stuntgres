package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexLocks(t *testing.T) {
	for _, writerCommit := range []bool{false, true} {
		for _, builderCommit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("writer=%t/builder=%t/held=%t", writerCommit, builderCommit, held), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							a, c := fixture.LockConnections(t, p.Ctx, b.DB)
							runA := transactionalIndexRun(t, p, b, a)
							runC := transactionalIndexRun(t, p, b, c)
							runA(fixture.Exec("CREATE TABLE t(n INT)", 0))
							runA(fixture.Exec("INSERT INTO t VALUES(1)", 1))
							ap, cp := 0, 0
							var oid int64
							if b.PG {
								ap = fixture.LockPID(t, p.Ctx, a)
								cp = fixture.LockPID(t, p.Ctx, c)
								oid = fixture.LockOID(t, p.Ctx, "t")
							}
							q := "CREATE INDEX idx ON t(n)"
							var e fixture.Executor = c
							if held {
								stmt, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							}
							runA(fixture.Exec("BEGIN", 0))
							defer a.ExecContext(p.Ctx, "ROLLBACK")
							runA(fixture.Exec("INSERT INTO t VALUES(2)", 1))
							runC(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							done := make(chan struct{})
							await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								defer close(done)
								r, err := e.ExecContext(ctx, q)
								return fixture.RelationOutcome{Result: r, Err: err}
							})
							fixture.RelationLockPending(t, p, b, cp, ap, oid, "ShareLock", done)
							if writerCommit {
								runA(fixture.Exec("COMMIT", 0))
							} else {
								runA(fixture.Exec("ROLLBACK", 0))
							}
							o := await()
							fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
							rows := [][]any{{int64(1)}}
							if writerCommit {
								rows = append(rows, []any{int64(2)})
							}
							runC(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
							runA(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
							writeDone := make(chan struct{})
							write := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								defer close(writeDone)
								r, err := a.ExecContext(ctx, "INSERT INTO t VALUES(3)")
								return fixture.RelationOutcome{Result: r, Err: err}
							})
							fixture.RelationLockPending(t, p, b, ap, cp, oid, "RowExclusiveLock", writeDone)
							if builderCommit {
								runC(fixture.Exec("COMMIT", 0))
							} else {
								runC(fixture.Exec("ROLLBACK", 0))
							}
							o = write()
							fixture.DeleteReferenceCount(t, o.Result, o.Err, 1)
							runA(transactionalIndexName("idx", builderCommit))
							rows = append(rows, []any{int64(3)})
							runA(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
						})
					}
				})
			}
		}
	}
	// Distinct parents distinguish premark retention from postmark release.
	t.Run("savepoint-lock-checkpoint", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := transactionalIndexRun(t, p, b, c)
				for _, q := range []string{"CREATE TABLE early(n INT)", "CREATE TABLE later(n INT)", "BEGIN", "CREATE INDEX early_idx ON early(n)", "SAVEPOINT mark", "CREATE INDEX later_idx ON later(n)"} {
					run(fixture.Exec(q, 0))
				}
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				ap, cp := 0, 0
				var early, later int64
				if b.PG {
					ap = fixture.LockPID(t, p.Ctx, a)
					cp = fixture.LockPID(t, p.Ctx, c)
					early = fixture.LockOID(t, p.Ctx, "early")
					later = fixture.LockOID(t, p.Ctx, "later")
				}
				done := make(chan struct{})
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					r, err := a.ExecContext(ctx, "INSERT INTO later VALUES(2)")
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				fixture.RelationLockPending(t, p, b, ap, cp, later, "RowExclusiveLock", done)
				run(fixture.Exec("ROLLBACK TO mark", 0))
				o := await()
				fixture.DeleteReferenceCount(t, o.Result, o.Err, 1)
				done = make(chan struct{})
				await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					r, err := a.ExecContext(ctx, "INSERT INTO early VALUES(1)")
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				fixture.RelationLockPending(t, p, b, ap, cp, early, "RowExclusiveLock", done)
				run(fixture.Exec("ROLLBACK TO mark", 0))
				run(fixture.Exec("ROLLBACK", 0))
				o = await()
				fixture.DeleteReferenceCount(t, o.Result, o.Err, 1)
			})
		}
	})
}
