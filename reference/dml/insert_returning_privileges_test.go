package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceInsertReturningPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"insert_returning_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			if b.PG {
				fixture.LockExec(t, p.Ctx, a, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
			}
			for _, q := range []string{"CREATE USER insert_returning_reader", "CREATE TABLE t(id INT,v TEXT)", "GRANT INSERT ON t TO insert_returning_reader", "SET SESSION AUTHORIZATION insert_returning_reader"} {
				fixture.LockExec(t, p.Ctx, a, q, 0)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("INSERT INTO t VALUES(1,'constant') RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}})
				s.Prepared = prepared
				run(s)
			}
			deny := func(q string) {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
					s.Prepared = prepared
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				}
			}
			for _, output := range []string{"id", "old.id", "new.id", "*", "old.*", "new.*", "old", "new", "CASE WHEN FALSE THEN v ELSE 'safe' END"} {
				deny("INSERT INTO t VALUES(2,'denied') RETURNING " + output)
				deny("INSERT INTO t SELECT 2,'denied' WHERE FALSE RETURNING " + output)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "constant"}, {int64(1), "constant"}}))
			fixture.LockExec(t, p.Ctx, a, "GRANT SELECT(id) ON t TO insert_returning_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION insert_returning_reader", 0)
			for _, output := range []string{"id", "old.id", "new.id"} {
				want := any(int64(3))
				if output == "old.id" {
					want = nil
				}
				run(fixture.Select("INSERT INTO t VALUES(3,'allowed') RETURNING "+output, []string{"id"}, []string{"INT4"}, [][]any{{want}}))
			}
			for _, output := range []string{"v", "old.v", "new.v", "*", "old", "new", "CASE WHEN FALSE THEN v ELSE 'safe' END"} {
				deny("INSERT INTO t VALUES(4,'denied') RETURNING " + output)
			}
			q := "INSERT INTO t VALUES($1,'held') RETURNING new.id"
			held, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}, 5))
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, p.Ctx, a, "REVOKE SELECT(id) ON t FROM insert_returning_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION insert_returning_reader", 0)
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, 6)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: q}, b.Recorder, s)
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(3)}, {int64(3)}, {int64(3)}, {int64(5)}}))
			// Arbiter SELECT and UPDATE rights are separate from RETURNING reads.
			for _, q := range []string{"CREATE TABLE conflicts(id INT PRIMARY KEY,v TEXT)", "GRANT INSERT,UPDATE ON conflicts TO insert_returning_reader", "GRANT SELECT(id) ON conflicts TO insert_returning_reader"} {
				fixture.LockExec(t, p.Ctx, a, q, 0)
			}
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO conflicts VALUES(1,'sentinel')", 1)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION insert_returning_reader", 0)
			deny("INSERT INTO t VALUES(7,'denied') RETURNING v")
			for _, q := range []string{"INSERT INTO conflicts VALUES(1,'skip') ON CONFLICT DO NOTHING RETURNING old.v", "INSERT INTO conflicts VALUES(1,'skip') ON CONFLICT(id) DO UPDATE SET v='skip' WHERE FALSE RETURNING new.v"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table conflicts"})
				s.SelectRows = true
				run(s)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			run(fixture.Select("SELECT id,v FROM conflicts", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
		})
	}
}

func TestReferenceInsertReturningZeroColumnPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"insert_returning_empty_reader", "insert_returning_empty_held"})
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		reader := "insert_returning_empty_reader"
		if prepared {
			reader = "insert_returning_empty_held"
		}
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		if b.PG {
			fixture.LockExec(t, p.Ctx, a, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
		}
		for _, q := range []string{"CREATE USER " + reader, "CREATE TABLE empty()", "GRANT INSERT ON empty TO " + reader} {
			fixture.LockExec(t, p.Ctx, a, q, 0)
		}
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO empty DEFAULT VALUES", 1)
		fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION "+reader, 0)
		constant := fixture.Select("INSERT INTO empty DEFAULT VALUES RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}})
		constant.Prepared = prepared
		run(constant)
		for _, output := range []string{"old", "new", "empty"} {
			for _, source := range []string{"DEFAULT VALUES", "SELECT WHERE FALSE"} {
				q := "INSERT INTO empty " + source + " RETURNING " + output
				s := fixture.ImplicitError(q, fixture.Diagnostic{
					Code: "42501", Message: "permission denied for table empty",
					Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "",
					Context: "", Schema: "", Table: "", Column: "", DataType: "", Constraint: "",
				})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				run(s)
				fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
				run(fixture.Select("SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
				fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION "+reader, 0)
			}
		}
		fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
		fixture.LockExec(t, p.Ctx, a, "GRANT SELECT ON empty TO "+reader, 0)
		fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION "+reader, 0)
		for _, output := range []string{"old", "new", "empty"} {
			value := any("()")
			if output == "old" {
				value = nil
			}
			s := fixture.Select("INSERT INTO empty DEFAULT VALUES RETURNING "+output, []string{output}, []string{""}, [][]any{{value}})
			s.Prepared = prepared
			run(insertReturningComposite(t, b, s))
			s = fixture.Select("INSERT INTO empty SELECT WHERE FALSE RETURNING "+output, []string{output}, []string{""}, [][]any{})
			s.Prepared = prepared
			run(insertReturningComposite(t, b, s))
		}
		fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
		run(fixture.Select("SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
	})
}

func TestReferenceInsertReturningPrivilegesAfterWait(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"insert_returning_wait_reader", "insert_returning_wait_held"})
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		reader := "insert_returning_wait_reader"
		if prepared {
			reader = "insert_returning_wait_held"
		}
		holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
		admin := fixture.ConcurrentIndexConn(t, p, b)
		if b.PG {
			fixture.LockExec(t, p.Ctx, holder, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
		}
		// PostgreSQL requires SELECT(v) for excluded.v too; secret remains
		// exclusively an output read, independent of arbiter and SET demands.
		for _, q := range []string{"CREATE USER " + reader, "CREATE TABLE t(id INT PRIMARY KEY,v INT,secret TEXT)", "GRANT INSERT,UPDATE ON t TO " + reader, "GRANT SELECT(id,v,secret) ON t TO " + reader} {
			fixture.LockExec(t, p.Ctx, holder, q, 0)
		}
		fixture.LockExec(t, p.Ctx, holder, "INSERT INTO t VALUES(1,10,'sentinel')", 1)
		fixture.LockExec(t, p.Ctx, worker, "SET SESSION AUTHORIZATION "+reader, 0)
		q := "INSERT INTO t VALUES(1,20,'proposal') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING secret"
		var x fixture.Executor = worker
		if prepared {
			held, err := worker.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			x = fixture.HeldStatement{Executor: worker, Statement: held, SQL: q}
		}
		var holderPID, workerPID int
		if b.PG {
			holderPID = fixture.LockPID(t, p.Ctx, holder)
			workerPID = fixture.LockPID(t, p.Ctx, worker)
		}
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM t FOR UPDATE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := x.QueryContext(ctx, q)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		if b.PG {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			fixture.RowWait(t, ctx, workerPID, holderPID)
		}
		// Native has no public queue observer. The private companion proves the
		// causal postwait check; this public route asserts denial, not queue entry.
		fixture.LockExec(t, p.Ctx, admin, "REVOKE SELECT(secret) ON t FROM "+reader, 0)
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		o := await()
		if !b.PG {
			if o.Rows != nil {
				o.Rows.Close()
				t.Fatal("native published output after output-only SELECT revocation")
			}
			fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
		} else {
			if o.Err != nil || o.Rows == nil {
				t.Fatal("executor-start rights should survive observed wait", o.Err)
			}
			defer o.Rows.Close()
			if !o.Rows.Next() {
				t.Fatal(o.Rows.Err())
			}
			var secret string
			if err := o.Rows.Scan(&secret); err != nil || secret != "sentinel" {
				t.Fatal(secret, err)
			}
			if o.Rows.Next() || o.Rows.Err() != nil {
				t.Fatal("unexpected output tail", o.Rows.Err())
			}
			o.Rows.Close()
		}
		want := int64(10)
		if b.PG {
			want = 20
		}
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Select("SELECT id,v,secret FROM t", []string{"id", "v", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), want, "sentinel"}}))
	})
}
