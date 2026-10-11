package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictUpdateInputs(t *testing.T) {
	for _, source := range []string{"VALUES(1,2),(2,3)", "SELECT k,v FROM s", "(SELECT k,v FROM s)", "SELECT k,v FROM s UNION SELECT k,v FROM s", "SELECT k,v FROM s INTERSECT SELECT k,v FROM s", "SELECT k,v FROM s EXCEPT SELECT 3,4", "SELECT k,v FROM s WHERE TRUE"} {
		t.Run(source, func(t *testing.T) {
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("CREATE TABLE s(k INT,v INT)", 0), fixture.Exec("INSERT INTO s VALUES(1,2),(2,3)", 2), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
				fixture.Exec("INSERT INTO t "+source+" ON CONFLICT(k) DO UPDATE SET v=excluded.v", 2), conflictUpdateRows([]any{int64(1), int64(2)}, []any{int64(2), int64(3)}))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT DEFAULT 1 UNIQUE,v INT DEFAULT 8)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t DEFAULT VALUES ON CONFLICT(k) DO UPDATE SET v=excluded.v", 1),
		fixture.Exec("INSERT INTO t(k,v) VALUES(DEFAULT,DEFAULT) ON CONFLICT(k) DO UPDATE SET v=excluded.v", 1), conflictUpdateRows([]any{int64(1), int64(8)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT GENERATED ALWAYS AS IDENTITY UNIQUE,v INT)", 0),
		fixture.Exec("INSERT INTO t OVERRIDING SYSTEM VALUE VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t OVERRIDING USER VALUE VALUES(99,2) ON CONFLICT(k) DO UPDATE SET v=excluded.k+excluded.v", 1),
		fixture.Exec("INSERT INTO t(v) VALUES(3) ON CONFLICT(k) DO UPDATE SET v=excluded.v", 1), conflictUpdateRows([]any{int64(1), int64(3)}, []any{int64(2), int64(3)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,n INT,g INT GENERATED ALWAYS AS(n+1) STORED,v INT)", 0), fixture.Exec("INSERT INTO t(k,n,v) VALUES(1,10,0)", 1),
		fixture.Exec("INSERT INTO t(k,n) VALUES(1,20) ON CONFLICT(k) DO UPDATE SET n=excluded.n,v=excluded.g", 1),
		fixture.Select("SELECT n,g,v FROM t", []string{"n", "g", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(20), int64(21), int64(21)}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE SEQUENCE seq", 0), fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT DEFAULT nextval('seq'))", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t(k) VALUES(1) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE", 0),
		fixture.Select("SELECT nextval('seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT DEFAULT 1/0)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.ErrorDiagnostic("INSERT INTO t(k) VALUES(1) ON CONFLICT(k) DO UPDATE SET v=2 WHERE FALSE", fixture.ControlsReferenceDiagnostic("22012", "division by zero")), conflictUpdateRows([]any{int64(1), int64(10)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v VARCHAR(3),a INT[])", 0), fixture.Exec("INSERT INTO t VALUES(1,'old','{1,2}')", 1),
		fixture.Exec("INSERT INTO t VALUES(1,'new','{8,9}') ON CONFLICT(k) DO UPDATE SET v=excluded.v,a[2]=excluded.a[1]", 1),
		fixture.Select("SELECT v,a FROM t", []string{"v", "a"}, []string{"VARCHAR", "_INT4"}, [][]any{{"new", "{1,8}"}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v SMALLINT,n NUMERIC(4,1),r DOUBLE PRECISION)", 0), fixture.Exec("INSERT INTO t VALUES(1,1,1,0)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,0,0,2.5) ON CONFLICT(k) DO UPDATE SET v=excluded.r,n=excluded.r", 1),
		fixture.Select("SELECT v,n FROM t", []string{"v", "n"}, []string{"INT2", "NUMERIC"}, [][]any{{int64(2), "2.5"}}),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2,2,2),(1,0,0,40000) ON CONFLICT(k) DO UPDATE SET v=excluded.r", fixture.ControlsReferenceDiagnostic("22003", "smallint out of range")),
		fixture.Select("SELECT k,v FROM t", []string{"k", "v"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(2)}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE DOMAIN upsert_small AS SMALLINT", 0), fixture.Exec("CREATE TABLE t(k INT UNIQUE,v upsert_small,n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10,1)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,2,20) ON CONFLICT(k) DO UPDATE SET v=excluded.n", 1),
		fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT2"}, [][]any{{int64(20)}}),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2,2),(1,2,40000) ON CONFLICT(k) DO UPDATE SET v=excluded.n", fixture.ControlsReferenceDiagnostic("22003", "smallint out of range")),
		fixture.Select("SELECT k,v FROM t", []string{"k", "v"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(20)}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,n INT,g INT GENERATED ALWAYS AS(n+1) STORED)", 0), fixture.Exec("INSERT INTO t(k,n) VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t(k,n) VALUES(1,20) ON CONFLICT(k) DO UPDATE SET n=excluded.n,g=DEFAULT", 1),
		fixture.Select("SELECT n,g FROM t", []string{"n", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(20), int64(21)}}))
}
func TestReferenceInsertConflictUpdateParameters(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		q := "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=$1 WHERE t.v>$2"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, c := range []struct {
			v, limit any
			count    int64
		}{{nil, int64(0), 1}, {int64(20), nil, 0}, {int64(20), int64(-1), 0}} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, c.count, c.v, c.limit))
			})
		}
		run(conflictUpdateRows([]any{int64(1), nil}))
		run(fixture.Exec("UPDATE t SET v=10", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(30), int64(0)))
		run(conflictUpdateRows([]any{int64(1), int64(30)}))
		if r, e := stmt.ExecContext(p.Ctx, int64(1)); e == nil || r != nil {
			t.Fatal("wrong parameter count exposed success", r, e)
		}
		run(conflictUpdateRows([]any{int64(1), int64(30)}))
	})
	q := "INSERT INTO t VALUES(1,0) ON CONFLICT(k) DO UPDATE SET v=$1 WHERE $1"
	// SET resolves $1 to integer before WHERE checks that same typed occurrence.
	d := conflictUpdateDiagnostic(q, "42804", "argument of WHERE must be type boolean, not type integer", "", "")
	d.Position = "67"
	for _, prepared := range []bool{false, true} {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
			s := fixture.ErrorDiagnostic(q, d, int64(1))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}
