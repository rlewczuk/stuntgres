package reference_test

import (
	"context"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceViewUpdatableContention(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("CREATE VIEW v AS SELECT id,a FROM t WHERE a>5", 0))
		run(fixture.Exec("CREATE VIEW w AS SELECT id,a FROM v WHERE id>0", 0))
		holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
		tx := fixture.QueryLockBegin(t, p.Ctx, holder)
		fixture.LockExec(t, p.Ctx, tx, "UPDATE t SET a=0 WHERE id=1", 1)
		fixture.LockExec(t, p.Ctx, waiter, "SET lock_timeout='20ms'", 0)
		out := fixture.RowExecute(p.Ctx, waiter, "UPDATE w SET a=99", held)
		if out.Err == nil {
			t.Fatal("blocked view write unexpectedly succeeded")
		}
		want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
		if b.PG {
			got := fixture.ExtractDiagnostic(t, out.Err)
			if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(got.Context) {
				t.Fatal("physical update context", got)
			}
			want.Context = got.Context
		}
		fixture.AssertDiagnostic(t, out.Err, &want)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		fixture.LockExec(t, p.Ctx, waiter, "SET lock_timeout=0", 0)
		// The external observer is PostgreSQL-only. Native queue/recheck proof belongs
		// in the production round's actual manager package-function tests.
		if b.PG {
			tx = fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.LockExec(t, p.Ctx, tx, "UPDATE t SET a=0 WHERE id=1", 1)
			pid, hpid := fixture.LockPID(t, p.Ctx, waiter), fixture.LockPID(t, p.Ctx, holder)
			done := fixture.RowAsync(t, p.Ctx, waiter, "UPDATE w SET a=99 WHERE id=1", held, false)
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			fixture.RowWait(t, ctx, pid, hpid)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			got := fixture.LockResult(t, done)
			if got.Err != nil {
				t.Fatal(got.Err)
			}
			fixture.LockAffected(t, got.Result, 0)
			run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(20)}}))
		}
	})
}
