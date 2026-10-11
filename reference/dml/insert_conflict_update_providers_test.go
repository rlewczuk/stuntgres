package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictUpdateProviders(t *testing.T) {
	for _, c := range []struct{ name, columns, index, target, seed, proposal string }{
		{"lower", "k TEXT,p TEXT", "lower(k) COLLATE \"C\" text_ops", "lower(k) COLLATE \"C\" text_ops", "'A','old'", "'a','new'"},
		{"expression", "k INT,p TEXT", "(k+1)", "(k+1)", "1,'old'", "1,'new'"},
		{"class_alias", "k TEXT,p TEXT", "k text_ops", "k varchar_ops", "'a','old'", "'a','new'"},
		{"bpchar_class", "k TEXT,p TEXT", "k bpchar_ops", "k", "'a','old'", "'a ','new'"},
		{"direction", "k INT,p TEXT", "k DESC NULLS FIRST", "k", "1,'old'", "1,'new'"},
		{"composite", "k INT,n INT,p TEXT", "k,n", "n,k,k", "1,2,'old'", "1,2,'new'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t("+c.columns+")", 0), fixture.Exec("CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+c.index+") INCLUDE(p)", 0),
				fixture.Exec("INSERT INTO t VALUES("+c.seed+")", 1), fixture.Exec("INSERT INTO t VALUES("+c.proposal+") ON CONFLICT("+c.target+") DO UPDATE SET p=excluded.p", 1),
				fixture.Select("SELECT p FROM t", []string{"p"}, []string{"TEXT"}, [][]any{{"new"}}))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT,active BOOL,n INT,p TEXT)", 0), fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE active AND n>0", 0), fixture.Exec("INSERT INTO t VALUES(1,TRUE,20,'old')", 1),
		fixture.Exec("INSERT INTO t VALUES(1,TRUE,20,'new'),(1,FALSE,0,'outside') ON CONFLICT(k) WHERE active AND n>10 DO UPDATE SET p=excluded.p WHERE t.n=excluded.n", 2),
		fixture.Select("SELECT p FROM t ORDER BY p", []string{"p"}, []string{"TEXT"}, [][]any{{"new"}, {"outside"}}),
		insertConflictNoMatch("INSERT INTO t SELECT 2,TRUE,0,'x' WHERE FALSE ON CONFLICT(k) WHERE active AND n>=0 DO UPDATE SET p=excluded.p"))
}
func TestReferenceInsertConflictUpdateAtomicity(t *testing.T) {
	for _, c := range []struct {
		name, ddl, q string
		d            fixture.Diagnostic
	}{
		{"not_null", "CREATE TABLE t(k INT UNIQUE,v INT NOT NULL)", "INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=NULL", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: "null value in column \"v\" of relation \"t\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "t", Column: "v"}},
		{"check", "CREATE TABLE t(k INT UNIQUE,v INT CONSTRAINT positive CHECK(v>0))", "INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=-excluded.v", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "new row for relation \"t\" violates check constraint \"positive\"", Detail: "Failing row contains (1, -20).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"arithmetic", "CREATE TABLE t(k INT UNIQUE,v INT)", "INSERT INTO t VALUES(2,2),(1,20),(1,0) ON CONFLICT(k) DO UPDATE SET v=10/excluded.v", fixture.ControlsReferenceDiagnostic("21000", "ON CONFLICT DO UPDATE command cannot affect row a second time")},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := fixture.ErrorDiagnostic(c.q, c.d)
			if c.name == "arithmetic" {
				s = conflictUpdateDuplicate(c.q)
			}
			conflictUpdateSteps(t, fixture.Exec(c.ddl, 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), s, conflictUpdateRows([]any{int64(1), int64(10)}), fixture.Exec("INSERT INTO t VALUES(4,4)", 1))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT UNIQUE)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(9,90)", 2),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=90", fixture.UniqueReferenceDuplicate("t", "t_v_key", "Key (v)=(90) already exists.")), conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(9), int64(90)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT,CONSTRAINT ex EXCLUDE USING btree(v WITH =))", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(9,90)", 2),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=90", fixture.ExclusionReferenceConflict("t", "ex", "v", "90", "90")), conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(9), int64(90)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(3,30)", 2),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2),(1,20),(3,0) ON CONFLICT(k) DO UPDATE SET v=10/excluded.v", fixture.ControlsReferenceDiagnostic("22012", "division by zero")), conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(3), int64(30)}),
		fixture.Exec("BEGIN", 0), fixture.Exec("SAVEPOINT recover", 0), conflictUpdateDuplicate("INSERT INTO t VALUES(2,2),(1,20),(1,30) ON CONFLICT(k) DO UPDATE SET v=excluded.v"),
		fixture.ErrorDiagnostic("SELECT k FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")),
		fixture.Exec("ROLLBACK TO SAVEPOINT recover", 0), conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(3), int64(30)}),
		fixture.Exec("INSERT INTO t VALUES(2,2),(1,20) ON CONFLICT(k) DO UPDATE SET v=excluded.v", 2), fixture.Exec("COMMIT", 0), conflictUpdateRows([]any{int64(1), int64(20)}, []any{int64(2), int64(2)}, []any{int64(3), int64(30)}))
}
func TestReferenceInsertConflictUpdatePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"upsert_actor"})
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE upsert_actor", 0))
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v INT,secret TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,'secret')", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO upsert_actor", 0))
		}
		run(fixture.Exec("GRANT INSERT ON t TO upsert_actor", 0))
		run(fixture.Exec("GRANT SELECT(k) ON t TO upsert_actor", 0))
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
		assume := func() { run(fixture.Exec("SET SESSION AUTHORIZATION upsert_actor", 0)) }
		reset := func() { run(fixture.Exec("RESET SESSION AUTHORIZATION", 0)) }
		assume()
		for _, source := range []string{"VALUES(1,2,'x')", "VALUES(2,2,'x')", "SELECT 2,2,'x' WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO t "+source+" ON CONFLICT(k) DO UPDATE SET v=2 WHERE FALSE", denied)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
		reset()
		run(fixture.Exec("GRANT UPDATE(v) ON t TO upsert_actor", 0))
		assume()
		run(fixture.Exec("INSERT INTO t VALUES(1,2,'x') ON CONFLICT ON CONSTRAINT u DO UPDATE SET v=2", 1))
		for _, rhs := range []string{"t.v", "excluded.v"} {
			s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,3,'x') ON CONFLICT(k) DO UPDATE SET v="+rhs, denied))
			s.ErrorStage = "execute"
			run(s)
		}
		s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t SELECT 2,2,'x' WHERE FALSE ON CONFLICT(k) DO UPDATE SET v=2 WHERE t.secret='secret'", denied))
		s.ErrorStage = "execute"
		run(s)
		reset()
		run(fixture.Exec("GRANT SELECT(v) ON t TO upsert_actor", 0))
		assume()
		q := "INSERT INTO t VALUES(1,3,'x') ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1))
		reset()
		run(fixture.Exec("REVOKE UPDATE(v) ON t FROM upsert_actor", 0))
		assume()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, denied))
		reset()
		run(fixture.Select("SELECT k,v,secret FROM t", []string{"k", "v", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(5), "secret"}}))
	})
	fixture.RoleReferenceCleanup(t, []string{"upsert_wait_actor"})
	t.Run("postwait_revoke", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			admin := fixture.ConcurrentIndexConn(t, p, b)
			fixture.LockExec(t, p.Ctx, admin, "CREATE ROLE upsert_wait_actor", 0)
			fixture.LockExec(t, p.Ctx, admin, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
			fixture.LockExec(t, p.Ctx, admin, "INSERT INTO t VALUES(1,10),(3,30)", 2)
			if b.PG {
				fixture.LockExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO upsert_wait_actor", 0)
			}
			fixture.LockExec(t, p.Ctx, admin, "GRANT INSERT,UPDATE,SELECT ON t TO upsert_wait_actor", 0)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t WHERE k=3 FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}}))
			ap, cp := 0, 0
			if b.PG {
				ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
			}
			fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION upsert_wait_actor", 0)
			q := "INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
			done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
			conflictUpdateWait(t, p, b, cp, ap)
			fixture.LockExec(t, p.Ctx, admin, "REVOKE UPDATE ON t FROM upsert_wait_actor", 0)
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			r := fixture.LockResult(t, done)
			want := [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}}
			// PostgreSQL checks executor startup permissions; native deliberately
			// rechecks current authority after suspension. Root must observe this split.
			if b.PG {
				fixture.State(t, r.Err, "")
				fixture.LockAffected(t, r.Result, 3)
				want = [][]any{{int64(1), int64(20)}, {int64(2), int64(2)}, {int64(3), int64(3)}}
			} else {
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
				fixture.AssertDiagnostic(t, r.Err, &d)
				if r.Result != nil {
					t.Fatal("denied result", r.Result)
				}
			}
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, conflictUpdateRows(want...))
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.LockExec(t, p.Ctx, c, "RESET SESSION AUTHORIZATION", 0)
		})
	})
}
