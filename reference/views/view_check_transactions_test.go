package reference_test

import (
	"context"
	"errors"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceViewCheckTransactions(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE a<b WITH CHECK OPTION", 0))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		own := func(s fixture.Step) { s.Prepared = held; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		own(fixture.Exec("INSERT INTO v VALUES(2,20)", 1))
		own(fixture.Exec("SAVEPOINT prior", 0))
		own(viewCheckError("INSERT INTO v VALUES(3,30),(40,4)", "v", "40, 4"))
		s := fixture.ImplicitError("SELECT a,b FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		if held {
			s.ErrorStage = "prepare"
		}
		own(s)
		run(viewCheckPairRows([]any{int64(1), int64(10)}))
		own(fixture.Exec("ROLLBACK TO prior", 0))
		own(viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
		own(fixture.Exec("INSERT INTO v VALUES(3,30)", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
	})
}

func TestReferenceViewCheckCancellation(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE a<b WITH CHECK OPTION", 0))
		q := "UPDATE v SET a=a+1"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := e.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
		if b.PG {
			holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.LockExec(t, p.Ctx, tx, "UPDATE t SET b=21 WHERE a=2", 1)
			pid, hpid := fixture.LockPID(t, p.Ctx, waiter), fixture.LockPID(t, p.Ctx, holder)
			live, stop := context.WithCancel(p.Ctx)
			defer stop()
			done := fixture.RowAsync(t, live, waiter, "UPDATE v SET b=b+1", held, false)
			wait, finish := context.WithTimeout(p.Ctx, 5*time.Second)
			defer finish()
			fixture.RowWait(t, wait, pid, hpid)
			stop()
			out := fixture.LockResult(t, done)
			if out.Err == nil {
				t.Fatal("cancelled pending write succeeded")
			}
			got := fixture.ExtractDiagnostic(t, out.Err)
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(got.Context) {
				t.Fatal("pending cancellation tuple context", got)
			}
			want.Context = got.Context
			fixture.AssertDiagnostic(t, out.Err, &want)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(viewCheckPairRows([]any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
		}
		run(fixture.Exec("UPDATE v SET b=b+1", 2), viewCheckPairRows([]any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
	})
}
