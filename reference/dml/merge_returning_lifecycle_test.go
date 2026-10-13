package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMergeReturningAtomicity(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'1'),(2,'bad'),(9,'sentinel')", 3))
		sentinel := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}, {int64(2), "bad"}, {int64(9), "sentinel"}})
		base := "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v RETURNING "
		for _, query := range []bool{false, true} {
			for _, tc := range []struct {
				out string
				d   fixture.Diagnostic
			}{
				{"v::int", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}},
				{"10/(2-t.id)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
				{"(SELECT x.id FROM (VALUES(1),(2)) x(id) WHERE t.id=2)", fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"}},
			} {
				insertReturningFailure(t, p, b, a, base+tc.out, prepared, query, tc.d)
				run(sentinel)
			}
		}
		for _, savepoint := range []bool{false, true} {
			tx, e := a.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(8,'kept')", 1)
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
			}
			insertReturningFailure(t, p, b, tx, base+"10/(2-t.id)", prepared, true, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO kept", 0)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=8", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
			} else {
				s := fixture.ImplicitError("SELECT * FROM t", fixture.TransactionAborted())
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			run(sentinel)
		}
		// pq may invalidate its physical connection on precancellation. Native's
		// eager cancellation and retained-session health are a native-only contract.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, e := a.QueryContext(ctx, base+"t.id")
			if rows != nil {
				rows.Close()
				t.Fatal("native cancellation exposed cursor")
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatal("native pre-cancel", e)
			}
			run(sentinel)
		}
	})
}
func TestReferenceMergeReturningVolatility(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE SEQUENCE draws START 101 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE t(id INT,v BIGINT NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		q := "MERGE INTO t USING (VALUES(1),(2),(3)) s(id) ON t.id=s.id WHEN MATCHED AND s.id=1 THEN UPDATE SET v=nextval('draws') WHEN NOT MATCHED THEN INSERT VALUES(s.id,nextval('draws')) RETURNING t.id,t.v,nextval('draws') AS draw"
		held, e := b.DB.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		run(fixture.Select("SELECT is_called FROM draws", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		run(insertReturningBag(q, []string{"id", "v", "draw"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(101), int64(102)}, {int64(3), int64(103), int64(104)}}, prepared))
		run(fixture.Select("SELECT nextval('draws')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(105)}}))
		failing := "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=CASE WHEN s.id=2 THEN NULL ELSE t.v END RETURNING nextval('draws')"
		d := fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "t", Column: "v"}
		insertReturningFailure(t, p, b, b.DB, failing, prepared, true, d)
		// Matched actions have no portable order: zero or one accepted target1 output
		// can precede target2's failure. The failing candidate itself draws nothing.
		var next int64
		if e := b.DB.QueryRowContext(p.Ctx, "SELECT nextval('draws')").Scan(&next); e != nil || next < 106 || next > 107 {
			t.Fatal("matched failure draw bound", next, e)
		}
		run(fixture.Exec("ALTER SEQUENCE draws RESTART WITH 107", 0))
		// A source-only ordered VALUES producer supplies a distinct proposal-order
		// witness; accepted INSERT output must precede the next proposal's NOT NULL error.
		failing = "MERGE INTO t USING (SELECT id,v FROM (VALUES(4,10::bigint),(5,NULL::bigint)) x(id,v) ORDER BY id) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v) RETURNING nextval('draws')"
		d.Detail = "Failing row contains (5, null)."
		insertReturningFailure(t, p, b, b.DB, failing, prepared, true, d)
		run(fixture.Select("SELECT nextval('draws')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(108)}}))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(20)}, {int64(3), int64(103)}}))
	})
}
func TestReferenceMergeReturningPrepared(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA merge_ret_high", "CREATE SCHEMA merge_ret_low", "CREATE TABLE public.t(id INT,v INT)", "CREATE TABLE merge_ret_low.labels(v TEXT)", "SET search_path=merge_ret_high,merge_ret_low,public"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO merge_ret_low.labels VALUES('original')", 1))
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND $1::boolean THEN UPDATE SET v=t.v+1 RETURNING old.v,new.v,$2 AS label,(SELECT v FROM labels) AS resolved"
		held, e := a.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		run(fixture.Exec("CREATE TABLE merge_ret_high.labels(v TEXT)", 0))
		run(fixture.Exec("INSERT INTO merge_ret_high.labels VALUES('shadow')", 1))
		x := fixture.HeldStatement{Executor: a, Statement: held, SQL: q}
		for _, flag := range []any{false, nil, true} {
			rows := [][]any{}
			if flag == true {
				rows = [][]any{{int64(10), int64(11), "fresh", "original"}}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"v", "v", "label", "resolved"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, rows, flag, "fresh"))
		}
		s := fixture.Select(q, []string{"v", "v", "label", "resolved"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(11), int64(12), nil, "shadow"}}, true, nil)
		s.Prepared = prepared
		run(s)
		run(fixture.Exec("PREPARE mr(BOOLEAN,TEXT) AS "+q, 0))
		run(fixture.Select("EXECUTE mr(TRUE,'sql')", []string{"v", "v", "label", "resolved"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(12), int64(13), "sql", "shadow"}}))
		run(fixture.Exec("DEALLOCATE mr", 0))
		star, e := a.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=99 RETURNING t.*")
		if e != nil {
			t.Fatal(e)
		}
		defer star.Close()
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN v TO renamed", 0))
		rows, e := star.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("invalid SET exposed cursor")
		}
		fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "v" of relation "t" does not exist`, Position: "80"})
		run(fixture.Select("SELECT renamed FROM t", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(13)}}))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN renamed TO v", 0))
		shape, e := a.PrepareContext(p.Ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=99 RETURNING t.*")
		if e != nil {
			t.Fatal(e)
		}
		defer shape.Close()
		run(fixture.Exec("ALTER TABLE t ADD COLUMN extra INT", 0))
		rows, e = shape.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("shape change exposed cursor")
		}
		fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"})
		run(fixture.Select("SELECT v,extra FROM t", []string{"v", "extra"}, []string{"INT4", "INT4"}, [][]any{{int64(13), nil}}))
	}, func(t *testing.T) { fixture.SchemaReferenceCleanup(t, "merge_ret_high", "merge_ret_low") })
}
func TestReferenceMergeReturningConcurrency(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		holder, worker := fixture.LockConnections(t, ctx, b.DB)
		q := "MERGE INTO t USING (VALUES(1,7)) s(id,delta) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+s.delta RETURNING merge_action(),s.delta,old.v,new.v"
		var held *sql.Stmt
		if prepared {
			var e error
			held, e = worker.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
		}
		hp, wp := 0, 0
		if b.PG {
			hp = fixture.LockPID(t, ctx, holder)
			wp = fixture.LockPID(t, ctx, worker)
		}
		fixture.LockExec(t, ctx, holder, "BEGIN", 0)
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		fixture.LockExec(t, ctx, holder, "UPDATE t SET v=40 WHERE id=1", 1)
		// Actual native pending/OLD proof belongs to the internal causal supplement;
		// the PG branch observes server row wait, never launch-as-wait or sleeps.
		if !b.PG {
			mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
			fixture.LockExec(t, ctx, holder, "COMMIT", 0)
			s := fixture.Select(q, []string{"merge_action", "delta", "v", "v"}, []string{"TEXT", "INT4", "INT4", "INT4"}, [][]any{{"UPDATE", int64(7), int64(40), int64(47)}})
			x := fixture.Executor(worker)
			if held != nil {
				x = fixture.HeldStatement{Executor: worker, Statement: held, SQL: q}
			}
			fixture.RunStep(t, ctx, x, b.Recorder, s)
			return
		}
		await := fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome {
			var rows *sql.Rows
			var e error
			if held != nil {
				rows, e = held.QueryContext(c)
			} else {
				rows, e = worker.QueryContext(c, q)
			}
			return fixture.RelationOutcome{Rows: rows, Err: e}
		})
		fixture.RowWait(t, ctx, wp, hp)
		fixture.LockExec(t, ctx, holder, "COMMIT", 0)
		out := await()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		defer out.Rows.Close()
		names, e := out.Rows.Columns()
		if e != nil || len(names) != 4 || names[0] != "merge_action" || names[1] != "delta" || names[2] != "v" || names[3] != "v" {
			t.Fatal("wait output schema", names, e)
		}
		if !out.Rows.Next() {
			t.Fatal("wait output absent", out.Rows.Err())
		}
		var action string
		var delta, old, new int64
		if e = out.Rows.Scan(&action, &delta, &old, &new); e != nil || action != "UPDATE" || delta != 7 || old != 40 || new != 47 {
			t.Fatal("wait output", action, delta, old, new, e)
		}
		if out.Rows.Next() || out.Rows.Err() != nil {
			t.Fatal("wait output tail", out.Rows.Err())
		}
		out.Rows.Close()
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(47)}}))
	})
}
