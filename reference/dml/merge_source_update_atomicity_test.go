package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdateCardinality(t *testing.T) {
	for _, tc := range []struct {
		name, source, rhs string
		fail              bool
	}{
		{"twice", "(1,TRUE),(1,TRUE)", "t.v+1", true},
		{"sameValue", "(1,TRUE),(1,TRUE)", "t.v", true},
		{"secondUnselected", "(1,TRUE),(1,FALSE)", "t.v+1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				q := "MERGE INTO t USING (VALUES" + tc.source + ") s(id,active) ON t.id=s.id WHEN MATCHED AND s.active THEN UPDATE SET v=" + tc.rhs + " WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"
				want := [][]any{{int64(1), int64(11)}, {int64(2), int64(120)}}
				if tc.fail {
					s := fixture.ImplicitError(q, mergeUpdateCardinalityDiagnostic())
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				} else {
					run(fixture.Exec(q, 2))
				}
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceMergeSourceUpdateConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, set string
		d              fixture.Diagnostic
	}{
		{"division", "CREATE TABLE t(id INT,v INT)", "v=10/(2-t.id)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"range", "CREATE TABLE t(id INT,v INT)", "v=CASE WHEN t.id=1 THEN 11::bigint ELSE 2147483648::bigint END", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		{"notNull", "CREATE TABLE t(id INT,v INT NOT NULL)", "v=CASE WHEN t.id=1 THEN 11 ELSE NULL END", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "t", Column: "v"}},
		{"check", "CREATE TABLE t(id INT,v INT CONSTRAINT positive CHECK(v>0))", "v=CASE WHEN t.id=1 THEN 11 ELSE -1 END", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (2, -1).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"unique", "CREATE TABLE t(id INT,v INT CONSTRAINT unique_v UNIQUE)", "v=11", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "unique_v"`, Detail: "Key (v)=(11) already exists.", Schema: "public", Table: "t", Constraint: "unique_v"}},
		{"scalarCardinality", "CREATE TABLE t(id INT,v INT)", "v=(SELECT x FROM (VALUES(1),(2)) independent(x))", fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET "+tc.set, tc.d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}

func TestReferenceMergeSourceUpdateAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, arms string }{
		{"targetOnlyFault", "WHEN MATCHED THEN UPDATE SET v=11 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,30) WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=100/(t.id-2)"},
		{"matchedFault", "WHEN MATCHED THEN UPDATE SET v=100/(t.id-1) WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=22 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,30)"},
		{"insertFault", "WHEN MATCHED THEN UPDATE SET v=11 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=22 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,100/(s.id-3))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1),(3)) s(id) ON t.id=s.id "+tc.arms, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				run(s)
				// No claim about unspecified target scan/action order is needed.
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}
