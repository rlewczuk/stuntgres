package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsConcurrentDDL(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(1,'first'),(2,'second')", 2))
		a, z := fixture.LockConnections(t, p.Ctx, b.DB)
		holderPID, waiterPID := foreignActionsPIDs(t, p, b, a, z)
		var ddl *sql.Conn
		if b.PG {
			var err error
			ddl, err = b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer ddl.Close()
			fixture.LockExec(t, p.Ctx, ddl, "SET lock_timeout='100ms'", 0)
		}
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.RowAffected(t, p.Ctx, holder, "UPDATE c SET note='latest' WHERE pid=1", false, 1)
		if !b.PG {
			// Ordinary native DROP has no AX admission; explicit-transaction
			// DROP retains its real capability boundary. A savepoint preserves
			// the genuine child holder and its private payload for the action.
			fixture.LockExec(t, p.Ctx, holder, "SAVEPOINT drop_boundary", 0)
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, foreignKeyFail("DROP TABLE c", foreignKeyError("0A000", "statement is unsupported in an explicit transaction", "", "", ""), false))
			fixture.LockExec(t, p.Ctx, holder, "ROLLBACK TO drop_boundary", 0)
		}
		q := "DELETE FROM p WHERE id=$1"
		stmt, err := z.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		started := make(chan struct{})
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			close(started)
			r, e := stmt.ExecContext(ctx, 1)
			return fixture.RelationOutcome{Result: r, Err: e}
		})
		<-started
		foreignActionsWait(t, p, b, waiterPID, holderPID)
		if b.PG {
			// Real PostgreSQL AX cannot bypass the admitted child RX owners.
			fixture.RunStep(t, p.Ctx, ddl, b.Recorder, foreignKeyFail("DROP TABLE c", foreignKeyError("55P03", "canceling statement due to lock timeout", "", "", ""), false))
			fixture.LockExec(t, p.Ctx, ddl, "SET lock_timeout=0", 0)
		}
		// Native child RX/row requests and cancel/replacement fences have
		// deterministic private/public complements, not fabricated DROP AX.
		if err = holder.Commit(); err != nil {
			t.Fatal(err)
		}
		o := join()
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		fixture.LockAffected(t, o.Result, 1)
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
		run(fixture.Exec("DROP TABLE c", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT replacement_fk REFERENCES p ON DELETE RESTRICT)", 0))
		run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
		s := foreignKeyFail(q, foreignActionsRestrict("p", "c", "replacement_fk", `Key (id)=(2) is referenced from table "c".`), false)
		s.Args = []any{2}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: z, Statement: stmt, SQL: q}, b.Recorder, s)
		run(foreignKeyIntRows("SELECT id FROM p", "id", 2))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
	})
}

func TestReferenceForeignActionsCancellationDeadlock(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT REFERENCES p ON DELETE CASCADE,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(10,1,'first'),(20,2,'second')", 2))
		a, z := fixture.LockConnections(t, p.Ctx, b.DB)
		if b.PG {
			fixture.LockExec(t, p.Ctx, a, "SET deadlock_timeout='100ms'", 0)
			fixture.LockExec(t, p.Ctx, z, "SET deadlock_timeout='100ms'", 0)
		}
		x, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer x.Rollback()
		y, err := z.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer y.Rollback()
		fixture.RowAffected(t, p.Ctx, x, "DELETE FROM p WHERE id=1", false, 1)
		fixture.RowAffected(t, p.Ctx, y, "UPDATE c SET note='held' WHERE id=20", false, 1)
		var pids [2]int
		var xids [2]string
		if b.PG {
			for i, tx := range []*sql.Tx{x, y} {
				if err := tx.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),pg_current_xact_id()::text").Scan(&pids[i], &xids[i]); err != nil {
					t.Fatal(err)
				}
			}
		}
		first := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowExecute(ctx, x, "DELETE FROM p WHERE id=2", true)
		})
		foreignActionsWait(t, p, b, pids[0], pids[1])
		second := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			return fixture.RowExecute(ctx, y, "DELETE FROM p WHERE id=1", true)
		})
		out := [2]fixture.RelationOutcome{first(), second()}
		victim := -1
		for i, o := range out {
			if o.Err != nil {
				if victim != -1 {
					t.Fatal("both owners failed", out[0].Err, out[1].Err)
				}
				victim = i
			} else {
				fixture.LockAffected(t, o.Result, 1)
			}
		}
		if victim < 0 {
			t.Fatal("action deadlock had no victim")
		}
		d := fixture.ExtractDiagnostic(t, out[victim].Err)
		want := foreignKeyError("40P01", "deadlock detected", "", "", "")
		want.Hint = "See server log for query details."
		if b.PG {
			other := 1 - victim
			want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", pids[victim], xids[other], pids[other], pids[other], xids[victim], pids[victim])
			relation := "p"
			if victim == 0 {
				relation = "c"
			}
			// Physical tuple coordinates are external executor metadata. Match
			// the complete released grammar and the actual violating relation;
			// never assign engine row IDs PostgreSQL heap coordinates.
			pattern := regexp.MustCompile(`^while deleting tuple \([0-9]+,[1-9][0-9]*\) in relation "` + relation + `"$`)
			parts := strings.Split(d.Context, "\n")
			expectedLines := 1
			if victim == 0 {
				expectedLines = 2
			}
			if len(parts) != expectedLines || !pattern.MatchString(parts[0]) {
				t.Fatal("physical action deadlock context", d.Context)
			}
			if victim == 0 && parts[1] != `SQL statement "`+foreignActionsSQL("c", "pid", "CASCADE", "DELETE")+`"` {
				t.Fatal("action SPI context", d.Context)
			}
			want.Context = d.Context
			fixture.AssertDiagnostic(t, out[victim].Err, &want)
		} else {
			expectedContext := ""
			if victim == 0 {
				expectedContext = foreignActionsContext(want, foreignActionsSQL("c", "pid", "CASCADE", "DELETE")).Context
			}
			if d.Context != expectedContext {
				t.Fatal("native logical action context", d.Context, expectedContext)
			}
			var public *stuntgres.Error
			if !errors.As(out[victim].Err, &public) {
				t.Fatal(out[victim].Err)
			}
			logical := *public
			logical.Context = ""
			fixture.DeadlockPublicError(t, &logical, "")
		}
		if victim == 0 {
			if err = x.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = y.Commit(); err != nil {
				t.Fatal(err)
			}
			run(foreignKeyIntRows("SELECT id FROM p", "id", 2))
			run(fixture.Select("SELECT id,pid,note FROM c", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(20), int64(2), "held"}}))
		} else {
			if err = y.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = x.Commit(); err != nil {
				t.Fatal(err)
			}
			run(foreignKeyIntRows("SELECT id FROM p", "id"))
			run(foreignKeyIntRows("SELECT id FROM c", "id"))
		}
	})
}
