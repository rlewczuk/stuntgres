package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func conflictUpdateSteps(t *testing.T, steps ...fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for _, s := range steps {
					s.Prepared = prepared
					if s.State != "" && s.ErrorStage == "" {
						s.ErrorStage = "execute"
					}
					// PostgreSQL rejects Parse/Prepare in an already aborted transaction.
					if prepared && s.State == "25P02" && s.Query == "SELECT k FROM t" {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			})
		})
	}
}
func conflictUpdateDuplicate(q string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("21000", "ON CONFLICT DO UPDATE command cannot affect row a second time")
	d.Hint = "Ensure that no rows proposed for insertion within the same command have duplicate constrained values."
	return fixture.ErrorDiagnostic(q, d)
}
func conflictUpdateRows(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT k,v FROM t ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, rows)
}
func TestReferenceInsertConflictUpdateBaseline(t *testing.T) {
	for _, target := range []string{"(k)", "ON CONSTRAINT u"} {
		t.Run(target, func(t *testing.T) {
			conflictUpdateSteps(t,
				fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,a INT,b INT,p TEXT)", 0),
				fixture.Exec("INSERT INTO t VALUES(0,0,0,'dead'),(1,10,20,'old')", 2),
				fixture.Exec("DELETE FROM t WHERE k=0", 1),
				fixture.Exec("INSERT INTO t AS dst VALUES(1,100,200,'new'),(2,30,40,'fresh') ON CONFLICT "+target+" DO UPDATE SET a=dst.b,b=dst.a,p=excluded.p", 2),
				fixture.Select("SELECT k,a,b,p FROM t ORDER BY k", []string{"k", "a", "b", "p"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(20), int64(10), "new"}, {int64(2), int64(30), int64(40), "fresh"}}),
				fixture.Exec("INSERT INTO t VALUES(1,0,0,NULL) ON CONFLICT(k) DO UPDATE SET p=t.p", 1))
		})
	}
}
func TestReferenceInsertConflictUpdateScopes(t *testing.T) {
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE excluded(k INT UNIQUE,v INT)", 0),
		fixture.Exec("INSERT INTO excluded VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO excluded AS dst VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=dst.v+excluded.v", 1),
		fixture.Select("SELECT v FROM excluded", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t AS dst SELECT k,v+1 FROM t ON CONFLICT(k) DO UPDATE SET v=excluded.v+dst.v", 1), conflictUpdateRows([]any{int64(1), int64(21)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("CREATE TABLE s(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), fixture.Exec("INSERT INTO s VALUES(0),(1)", 2),
		fixture.Exec("INSERT INTO t SELECT t.k+s.n,t.v+1 FROM t CROSS JOIN s WHERE TRUE ON CONFLICT(k) DO UPDATE SET v=excluded.v", 2),
		conflictUpdateRows([]any{int64(1), int64(11)}, []any{int64(2), int64(11)}))
}
func TestReferenceInsertConflictUpdateWhere(t *testing.T) {
	for _, predicate := range []string{"FALSE", "NULL", "t.v<excluded.v"} {
		t.Run(predicate, func(t *testing.T) {
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
				fixture.Exec("INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=1/(excluded.v-1) WHERE "+predicate, 0),
				fixture.Exec("INSERT INTO t VALUES(2,2) ON CONFLICT(k) DO UPDATE SET v=0 WHERE "+predicate, 1),
				conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(2), int64(2)}))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,20) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE t.v<excluded.v", 1),
		fixture.Exec("INSERT INTO t SELECT 2,2 WHERE FALSE ON CONFLICT(k) DO UPDATE SET v=excluded.v", 0), conflictUpdateRows([]any{int64(1), int64(20)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT NOT NULL)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,NULL) ON CONFLICT(k) DO UPDATE SET v=2 WHERE FALSE", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: "null value in column \"v\" of relation \"t\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "t", Column: "v"}),
		conflictUpdateRows([]any{int64(1), int64(10)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT CONSTRAINT positive CHECK(v>0))", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,-1) ON CONFLICT(k) DO UPDATE SET v=2 WHERE FALSE", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: "new row for relation \"t\" violates check constraint \"positive\"", Detail: "Failing row contains (1, -1).", Schema: "public", Table: "t", Constraint: "positive"}),
		conflictUpdateRows([]any{int64(1), int64(10)}))
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=1/0 WHERE FALSE", fixture.ControlsReferenceDiagnostic("22012", "division by zero")),
		conflictUpdateRows([]any{int64(1), int64(10)}))
}
func TestReferenceInsertConflictUpdateDuplicates(t *testing.T) {
	for _, c := range []struct {
		name, values, predicate string
		count                   int64
		fail                    bool
		value                   int64
	}{
		{"false_false", "(1,0),(1,0)", "excluded.v>0", 0, false, 10},
		{"false_true", "(1,0),(1,2)", "excluded.v>0", 1, false, 2},
		{"true_false", "(1,2),(1,0)", "excluded.v>0", 0, true, 10},
		{"true_true", "(1,2),(1,3)", "TRUE", 0, true, 10},
		{"insert_false", "(2,2),(2,0)", "FALSE", 0, true, 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := "INSERT INTO t VALUES(3,3)," + c.values + " ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE " + c.predicate
			s := fixture.Exec(q, c.count+1)
			rows := [][]any{{int64(1), c.value}, {int64(3), int64(3)}}
			if c.fail {
				s = conflictUpdateDuplicate(q)
				rows = [][]any{{int64(1), int64(10)}}
			}
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), s, conflictUpdateRows(rows...))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v", 1), fixture.Exec("COMMIT", 0), conflictUpdateRows([]any{int64(1), int64(2)}))
	for _, typ := range []struct{ typ, a, b string }{{"NUMERIC", "1.0", "1.00"}, {"DOUBLE PRECISION", "'NaN'", "'NaN'"}, {"CHAR(4)", "'a'", "'a '"}} {
		t.Run(typ.typ, func(t *testing.T) {
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k "+typ.typ+" UNIQUE,v INT)", 0),
				conflictUpdateDuplicate("INSERT INTO t VALUES("+typ.a+",1),("+typ.b+",2) ON CONFLICT(k) DO UPDATE SET v=excluded.v"),
				fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
	for _, nnd := range []bool{false, true} {
		t.Run(fmt.Sprintf("nulls_not_distinct=%t", nnd), func(t *testing.T) {
			policy := ""
			if nnd {
				policy = "NULLS NOT DISTINCT"
			}
			q := "INSERT INTO t VALUES(NULL,1),(NULL,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
			s := fixture.Exec(q, 2)
			n := int64(2)
			if nnd {
				s = conflictUpdateDuplicate(q)
				n = 0
			}
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT,v INT,UNIQUE "+policy+"(k))", 0), s,
				fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
		})
	}
	for _, union := range []string{"UNION", "UNION ALL"} {
		t.Run(union, func(t *testing.T) {
			q := "INSERT INTO t SELECT 1,2 " + union + " SELECT 1,2 ON CONFLICT(k) DO UPDATE SET v=excluded.v"
			s := fixture.Exec(q, 1)
			v := int64(2)
			if union == "UNION ALL" {
				s = conflictUpdateDuplicate(q)
				v = 10
			}
			conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), s, conflictUpdateRows([]any{int64(1), v}))
		})
	}
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,2),(1,3) ON CONFLICT(k) DO UPDATE SET k=excluded.v", 2),
		conflictUpdateRows([]any{int64(1), int64(3)}, []any{int64(2), int64(10)}))
	// The two proposals reach the same row through different installed arbiters.
	conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE NULLS NOT DISTINCT,v INT)", 0), fixture.Exec("CREATE UNIQUE INDEX second ON t(k)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,10)", 1), conflictUpdateDuplicate("INSERT INTO t VALUES(1,2),(1,3) ON CONFLICT(k) DO UPDATE SET v=excluded.v"), conflictUpdateRows([]any{int64(1), int64(10)}))
}
func TestReferenceInsertConflictUpdateMetadata(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, plain := range []bool{false, true} {
			t.Run(fmt.Sprintf("held=%t/plain=%t", held, plain), func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { s.NonContext = plain; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					q := "INSERT INTO t VALUES($1,$2) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE excluded.v IS NOT NULL"
					var x fixture.Executor = b.DB
					if held {
						var stmt *sql.Stmt
						var err error
						if plain {
							stmt, err = b.DB.Prepare(q)
						} else {
							stmt, err = b.DB.PrepareContext(p.Ctx, q)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					}
					for _, c := range []struct {
						k int64
						v any
						n int64
					}{{1, int64(20), 1}, {2, nil, 1}, {1, nil, 0}, {1, int64(20), 1}} {
						s := fixture.Exec(q, c.n, c.k, c.v)
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					}
					s := fixture.Select(q, nil, nil, nil, int64(1), nil)
					s.NonContext = plain
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
						cols, e := r.Columns()
						if e != nil || len(cols) != 0 {
							t.Fatal(cols, e)
						}
						types, e := r.ColumnTypes()
						if e != nil || len(types) != 0 {
							t.Fatal(types, e)
						}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					run(conflictUpdateRows([]any{int64(1), int64(20)}, []any{int64(2), nil}))
					bad := "INSERT INTO t VALUES(3,3),(1,30),(1,40) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
					var failing fixture.Executor = b.DB
					if held {
						var stmt *sql.Stmt
						var err error
						if plain {
							stmt, err = b.DB.Prepare(bad)
						} else {
							stmt, err = b.DB.PrepareContext(p.Ctx, bad)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						failing = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: bad}
					}
					for _, query := range []bool{false, true} {
						s := conflictUpdateDuplicate(bad)
						s.NonContext = plain
						s.SelectRows = query
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, failing, b.Recorder, s)
					}
					run(conflictUpdateRows([]any{int64(1), int64(20)}, []any{int64(2), nil}))
				})
			})
		}
	}
}
