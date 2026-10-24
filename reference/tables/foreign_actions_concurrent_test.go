package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func foreignActionsWait(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int) {
	t.Helper()
	if b.PG {
		ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
		defer cancel()
		fixture.RowWait(t, ctx, waiter, holder)
	}
	// Native exact queue identity/modes are internal complements. Shared public
	// outcomes below do not infer a native queue from a sleep or scheduling order.
}

// Capture physical identities before a worker owns either connection. A PID
// query on a busy *sql.Conn would serialize behind the very wait being observed.
func foreignActionsPIDs(t *testing.T, p *fixture.Pair, b fixture.Backend, holder, waiter *sql.Conn) (int, int) {
	t.Helper()
	if !b.PG {
		return 0, 0
	}
	return fixture.LockPID(t, p.Ctx, holder), fixture.LockPID(t, p.Ctx, waiter)
}

func TestReferenceForeignActionsConcurrent(t *testing.T) {
	for _, childFirst := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("child-first=%t/commit=%t/prepared=%t", childFirst, commit, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
						run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON DELETE CASCADE)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1,'held'),(2,'free')", 2))
						a, z := fixture.LockConnections(t, p.Ctx, b.DB)
						holderPID, waiterPID := foreignActionsPIDs(t, p, b, a, z)
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
						finished := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							close(started)
							return fixture.RowExecute(ctx, waiter, waitQ, prepared)
						})
						<-started
						if childFirst && !b.PG {
							fixture.NativeDeleteRowWait(t, p.Ctx, finished)
						}
						foreignActionsWait(t, p, b, waiterPID, holderPID)
						run(fixture.Exec("UPDATE p SET note='independent' WHERE id=2", 1))
						if childFirst {
							run(fixture.Exec("UPDATE p SET note='compatible' WHERE id=1", 1))
						}
						if commit {
							err = holder.Commit()
						} else {
							err = holder.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						o := join()
						if !childFirst && commit {
							d := foreignKeyChild("c", "fk", `Key (pid)=(1) is not present in table "p".`)
							fixture.AssertDiagnostic(t, o.Err, &d)
							_, err = waiter.ExecContext(p.Ctx, "SELECT 1")
							d = fixture.TransactionAborted()
							fixture.AssertDiagnostic(t, err, &d)
							if err = waiter.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else {
							if o.Err != nil {
								t.Fatal(o.Err)
							}
							fixture.LockAffected(t, o.Result, 1)
							if err = waiter.Commit(); err != nil {
								t.Fatal(err)
							}
						}
						parents, children := []int64{2}, []int64(nil)
						if !childFirst && !commit {
							parents = []int64{1, 2}
							children = []int64{1}
						}
						run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", parents...))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", children...))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsConcurrentRefresh(t *testing.T) {
	for _, direction := range []string{"UPDATE", "DELETE"} {
		for _, move := range []bool{false, true} {
			for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead, sql.LevelSerializable} {
				t.Run(fmt.Sprintf("%s/move=%t/%s", direction, move, isolation), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT CONSTRAINT fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE,note TEXT)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
						run(fixture.Exec("INSERT INTO c VALUES(10,1,'old')", 1))
						a, z := fixture.LockConnections(t, p.Ctx, b.DB)
						holderPID, waiterPID := foreignActionsPIDs(t, p, b, a, z)
						waiter, err := z.BeginTx(p.Ctx, &sql.TxOptions{Isolation: isolation})
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						fixture.RunStep(t, p.Ctx, waiter, b.Recorder, foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						q := "UPDATE c SET note='latest' WHERE id=10"
						if move {
							q = "UPDATE c SET pid=2,note='latest' WHERE id=10"
						}
						fixture.RowAffected(t, p.Ctx, holder, q, false, 1)
						q = "DELETE FROM p WHERE id=1"
						if direction == "UPDATE" {
							q = "UPDATE p SET id=3 WHERE id=1"
						}
						started := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							close(started)
							return fixture.RowExecute(ctx, waiter, q, true)
						})
						<-started
						foreignActionsWait(t, p, b, waiterPID, holderPID)
						if err = holder.Commit(); err != nil {
							t.Fatal(err)
						}
						o := join()
						if isolation != sql.LevelReadCommitted {
							// execMain's stable-snapshot tuple update/delete conflict and
							// the enclosing release RI SPI query specify this diagnostic.
							d := foreignActionsContext(foreignKeyError("40001", "could not serialize access due to concurrent update", "", "", ""), foreignActionsSQL("c", "pid", "CASCADE", direction))
							fixture.AssertDiagnostic(t, o.Err, &d)
							if err = waiter.Rollback(); err != nil {
								t.Fatal(err)
							}
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
						} else {
							if o.Err != nil {
								t.Fatal(o.Err)
							}
							fixture.LockAffected(t, o.Result, 1)
							if err = waiter.Commit(); err != nil {
								t.Fatal(err)
							}
							parents := []int64{2}
							if direction == "UPDATE" {
								parents = []int64{2, 3}
							}
							run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", parents...))
						}
						var rows [][]any
						pid := int64(1)
						if move {
							pid = 2
						} else if direction == "UPDATE" && isolation == sql.LevelReadCommitted {
							pid = 3
						}
						if move || direction == "UPDATE" || isolation != sql.LevelReadCommitted {
							rows = [][]any{{int64(10), pid, "latest"}}
						}
						run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, rows))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT REFERENCES p ON DELETE CASCADE)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
			run(fixture.Exec("INSERT INTO c VALUES(10,1),(20,2)", 2))
			a, z := fixture.LockConnections(t, p.Ctx, b.DB)
			holder, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.RowAffected(t, p.Ctx, holder, "UPDATE c SET id=id WHERE id=20", false, 1)
			ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
			defer cancel()
			o := fixture.RowExecute(ctx, z, "DELETE FROM p", prepared)
			if b.PG {
				got := fixture.ExtractDiagnostic(t, o.Err)
				// Only the physical tuple coordinates vary in this cascade.
				pattern := regexp.MustCompile(`^while deleting tuple \([0-9]+,[1-9][0-9]*\) in relation "c"\nSQL statement "DELETE FROM ONLY "public"\."c" WHERE \$1 OPERATOR\(pg_catalog\.=\) "pid""$`)
				if !pattern.MatchString(got.Context) {
					t.Fatalf("blocked action interruption Context got %q", got.Context)
				}
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				want.Context = got.Context
				fixture.AssertDiagnostic(t, o.Err, &want)
			} else if !errors.Is(o.Err, context.DeadlineExceeded) || o.Result != nil {
				t.Fatal("blocked action did not cancel without a result", o.Err, o.Result)
			}
			<-ctx.Done()
			ctxErr := ctx.Err()
			if ctxErr != context.DeadlineExceeded {
				t.Fatal("blocked action context", ctxErr)
			}
			cancel()
			if err = z.Close(); err != nil {
				t.Fatal(err)
			}
			if err = holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
			run(foreignKeyIntRows("SELECT id FROM c ORDER BY id", "id", 10, 20))
			run(fixture.Exec("DELETE FROM p", 2))
			run(foreignKeyIntRows("SELECT id FROM c", "id"))
		})
	}
}

func TestReferenceForeignActionsCancellationTimeout(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		var childTuple string
		if b.PG {
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT ctid::text FROM c").Scan(&childTuple); err != nil {
				t.Fatal(err)
			}
		}
		a, z := fixture.LockConnections(t, p.Ctx, b.DB)
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.RowAffected(t, p.Ctx, holder, "UPDATE c SET pid=pid", false, 1)
		waiter, err := z.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer waiter.Rollback()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, waiter, b.Recorder, s) }
		step(fixture.Exec("SAVEPOINT keep", 0))
		step(fixture.Exec("SET LOCAL statement_timeout='100ms'", 0))
		d := foreignActionsContext(foreignKeyError("57014", "canceling statement due to statement timeout", "", "", ""), foreignActionsSQL("c", "pid", "CASCADE", "DELETE"))
		if b.PG {
			d.Context = `while deleting tuple ` + childTuple + ` in relation "c"` + "\n" + d.Context
		}
		step(foreignKeyFail("DELETE FROM p", d, false))
		step(foreignKeyFail("SELECT 1", fixture.TransactionAborted(), false))
		step(fixture.Exec("ROLLBACK TO keep", 0))
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		step(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
		step(fixture.Exec("DELETE FROM p", 1))
		if err = waiter.Commit(); err != nil {
			t.Fatal(err)
		}
		run(foreignKeyIntRows("SELECT pid FROM c", "pid"))
	})
}
