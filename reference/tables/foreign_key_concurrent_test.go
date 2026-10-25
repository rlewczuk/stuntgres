package reference_test

import (
	"context"
	"fmt"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceForeignKeyConcurrent(t *testing.T) {
	for _, childFirst := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("child-first=%t/commit=%t/prepared=%t", childFirst, commit, prepared), func(t *testing.T) {
					foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
						run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1,'a'),(2,'b')", 2))
						a, z := fixture.LockConnections(t, p.Ctx, b.DB)
						holderPID, waiterPID := 0, 0
						if b.PG {
							holderPID = fixture.LockPID(t, p.Ctx, a)
							waiterPID = fixture.LockPID(t, p.Ctx, z)
						}
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						waiter, err := z.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						holdQ, waitQ := "DELETE FROM p WHERE id=1", "INSERT INTO c VALUES(1)"
						if childFirst {
							holdQ, waitQ = waitQ, holdQ
						}
						fixture.RowAffected(t, p.Ctx, holder, holdQ, prepared, 1)
						started := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							close(started)
							err := fixture.TransactionConstraintExec(t, ctx, waiter, waitQ, prepared)
							return fixture.RelationOutcome{Err: err}
						})
						<-started
						if b.PG {
							ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
							for {
								var blocked bool
								err := fixture.Admin.QueryRowContext(ctx, "SELECT $2 = ANY(pg_blocking_pids($1))", waiterPID, holderPID).Scan(&blocked)
								if err != nil {
									cancel()
									t.Fatal(err)
								}
								if blocked {
									break
								}
								runtime.Gosched()
							}
							cancel()
						}
						// Native exact queue evidence is internal. These public operations
						// independently rule out serializing unrelated rows or non-key writes.
						run(fixture.Exec("UPDATE p SET note='free' WHERE id=2", 1))
						if childFirst {
							run(fixture.Exec("UPDATE p SET note='compatible' WHERE id=1", 1))
						}
						run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						if commit {
							err = holder.Commit()
						} else {
							err = holder.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := join()
						if commit {
							want := fixture.ForeignKeyChild("c", "fk", `Key (pid)=(1) is not present in table "p".`)
							if childFirst {
								want = fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`)
							}
							fixture.AssertDiagnostic(t, o.Err, &want)
							_, err = waiter.ExecContext(p.Ctx, "SELECT 1")
							aborted := fixture.TransactionAborted()
							fixture.AssertDiagnostic(t, err, &aborted)
							if err = waiter.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else {
							if o.Err != nil {
								t.Fatal(o.Err)
							}
							if err = waiter.Commit(); err != nil {
								t.Fatal(err)
							}
						}
						parent, child := []int64{2}, []int64(nil)
						if childFirst == commit {
							parent = []int64{1, 2}
							child = []int64{1}
						}
						run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", parent...))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", child...))
					})
				})
			}
		}
	}
}

func TestReferenceForeignKeyConcurrentCancellation(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		a, z := fixture.LockConnections(t, p.Ctx, b.DB)
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.RowAffected(t, p.Ctx, holder, "DELETE FROM p", false, 1)
		// A bounded context cancels the actual blocked child operation. This
		// public phase proves cleanup/data effects, not native queue identity.
		ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
		defer cancel()
		_, err = z.ExecContext(ctx, "INSERT INTO c VALUES(1)")
		if err == nil {
			t.Fatal("blocked insertion survived its cancellation")
		}
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatal("operation failed before cancellation", err, ctx.Err())
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
		// pq may discard the context-cancelled physical transport; healthy pool
		// recovery and an unblocked later write are the portable guarantees.
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
	})
}
