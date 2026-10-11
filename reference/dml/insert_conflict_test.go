package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func insertConflictScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func insertConflictSteps(t *testing.T, steps ...fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				for _, step := range steps {
					step.Prepared = prepared
					if step.State != "" && step.ErrorStage == "" {
						step.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
				}
			})
		})
	}
}
func insertConflictNoMatch(q string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification"))
	s.ErrorStage = "execute"
	return s
}
func insertConflictRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"k"}, []string{"INT4"}, rows)
}

func TestReferenceInsertConflictBaseline(t *testing.T) {
	for _, clause := range []string{"", "(k)", "ON CONSTRAINT u"} {
		t.Run(clause, func(t *testing.T) {
			suffix := " ON CONFLICT " + clause + " DO NOTHING"
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0),
				fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("INSERT INTO t VALUES(1),(2)"+suffix, 1),
				fixture.Exec("INSERT INTO t VALUES(1),(2)"+suffix, 0), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}),
				fixture.Exec("DELETE FROM t", 2), fixture.Exec("INSERT INTO t VALUES(1),(1),(2)"+suffix, 2),
				insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
		})
	}
}
func TestReferenceInsertConflictMultipleArbiters(t *testing.T) {
	for _, target := range []string{"(a)", "ON CONSTRAINT ua"} {
		t.Run(target, func(t *testing.T) {
			suffix := " ON CONFLICT " + target + " DO NOTHING"
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t(a INT,b INT,CONSTRAINT ub UNIQUE(b),CONSTRAINT ua UNIQUE(a))", 0),
				fixture.Exec("INSERT INTO t VALUES(1,10)", 1), fixture.Exec("INSERT INTO t VALUES(1,10)"+suffix, 0),
				fixture.Exec("INSERT INTO t VALUES(1,11),(2,20)"+suffix, 1),
				fixture.ErrorDiagnostic("INSERT INTO t VALUES(3,30),(4,10)"+suffix, fixture.UniqueReferenceDuplicate("t", "ub", "Key (b)=(10) already exists.")),
				fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		})
	}
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(a INT UNIQUE,b INT UNIQUE)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1),
		fixture.Exec("INSERT INTO t VALUES(1,11),(2,10),(3,30) ON CONFLICT DO NOTHING", 1),
		fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}}))
}
func TestReferenceInsertConflictInference(t *testing.T) {
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(a INT,b INT,c TEXT,UNIQUE(a,b))", 0), fixture.Exec("INSERT INTO t VALUES(1,2,'old')", 1),
		fixture.Exec("INSERT INTO t VALUES(1,2,'new') ON CONFLICT (b,a,a) DO NOTHING", 0),
		insertConflictNoMatch("INSERT INTO t VALUES(2,3,'x') ON CONFLICT (a) DO NOTHING"),
		insertConflictNoMatch("INSERT INTO t VALUES(2,3,'x') ON CONFLICT (a,b,c) DO NOTHING"),
		fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"old"}}))
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT)", 0), fixture.Exec("INSERT INTO t VALUES(1),(1) ON CONFLICT DO NOTHING", 2),
		insertConflictNoMatch("INSERT INTO t SELECT 2 WHERE FALSE ON CONFLICT (k) DO NOTHING"), insertConflictRows("SELECT k FROM t", []any{int64(1)}, []any{int64(1)}))
}
func TestReferenceInsertConflictTypedEquality(t *testing.T) {
	for _, c := range []struct{ name, typ, first, second string }{
		{"numeric", "NUMERIC", "1.0", "1.00"}, {"nan", "DOUBLE PRECISION", "'NaN'", "'NaN'"},
		{"zero", "DOUBLE PRECISION", "'0'", "'-0'"}, {"bpchar", "CHAR(4)", "'a'", "'a '"},
	} {
		t.Run(c.name, func(t *testing.T) {
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k "+c.typ+" UNIQUE,p TEXT)", 0),
				fixture.Exec("INSERT INTO t VALUES("+c.first+",'first')", 1), fixture.Exec("INSERT INTO t VALUES("+c.second+",'second') ON CONFLICT(k) DO NOTHING", 0),
				fixture.Select("SELECT p FROM t", []string{"p"}, []string{"TEXT"}, [][]any{{"first"}}))
		})
	}
	for _, nnd := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "not_distinct"}[nnd], func(t *testing.T) {
			policy := ""
			count := int64(2)
			if nnd {
				policy = "NULLS NOT DISTINCT"
				count = 1
			}
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT, p INT,UNIQUE "+policy+"(k,p))", 0),
				fixture.Exec("INSERT INTO t VALUES(NULL,1),(NULL,1) ON CONFLICT(k,p) DO NOTHING", count),
				fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
		})
	}
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT)", 0), fixture.Exec("CREATE UNIQUE INDEX permissive ON t(k)", 0),
		fixture.Exec("CREATE UNIQUE INDEX strict ON t(k) NULLS NOT DISTINCT", 0), fixture.Exec("INSERT INTO t VALUES(NULL),(NULL) ON CONFLICT(k) DO NOTHING", 1),
		insertConflictRows("SELECT k FROM t", []any{nil}))
}
func TestReferenceInsertConflictMetadata(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,p TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				s := fixture.Select("INSERT INTO t VALUES(1,'ignored') ON CONFLICT DO NOTHING", nil, nil, nil)
				s.Prepared, s.NonContext = prepared, plain
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					cols, err := r.Columns()
					if err != nil || len(cols) != 0 {
						t.Fatal(cols, err)
					}
					types, err := r.ColumnTypes()
					if err != nil || len(types) != 0 {
						t.Fatal(types, err)
					}
				}
				run(s)
			}
		}
		run(fixture.Exec("INSERT INTO t VALUES(1,'ignored'),(2,NULL) ON CONFLICT(k) DO NOTHING", 1))
		run(fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), nil}}))
	})
}
