package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteCardinality(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		n          int64
		fail       bool
		rows       [][]any
	}{
		{"deleteDelete", "WHEN MATCHED THEN DELETE", 0, true, nil},
		{"deleteUpdate", "WHEN MATCHED AND s.ord=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=20", 0, true, nil},
		{"updateDelete", "WHEN MATCHED AND s.ord=1 THEN UPDATE SET v=20 WHEN MATCHED THEN DELETE", 0, true, nil},
		{"trueFalse", "WHEN MATCHED AND s.ord=1 THEN DELETE", 1, false, nil},
		{"falseFalse", "WHEN MATCHED AND FALSE THEN DELETE", 0, false, [][]any{{int64(1), int64(10)}}},
		{"originalFrameTrue", "WHEN MATCHED AND s.ord=1 THEN UPDATE SET v=20 WHEN MATCHED AND t.v=10 THEN DELETE", 0, true, nil},
		{"originalFrameFalse", "WHEN MATCHED AND s.ord=1 THEN UPDATE SET v=20 WHEN MATCHED AND t.v=20 THEN DELETE", 1, false, [][]any{{int64(1), int64(20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,1),(2,1)", 2))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id=s.id " + tc.arms
				want := tc.rows
				if tc.fail {
					s := fixture.ImplicitError(q, mergeUpdateCardinalityDiagnostic())
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}}
				} else {
					run(fixture.Exec(q, tc.n))
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

// Proposed independent goldens: the root pins second-action fault precedence
// before authorizing any executor changes. Every witness has an earlier INSERT.
func TestReferenceMergeDeleteOraclePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, arms string
		d               fixture.Diagnostic
	}{
		{"deleteThenDivision", "CREATE TABLE t(id INT,v INT)", "WHEN MATCHED AND s.ord=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=10/s.x", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"deleteThenRange", "CREATE TABLE t(id INT,v INT)", "WHEN MATCHED AND s.ord=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.x", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		{"deleteThenNotNull", "CREATE TABLE t(id INT,v INT NOT NULL)", "WHEN MATCHED AND s.ord=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=NULL", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "t", Column: "v"}},
		{"deleteThenCheck", "CREATE TABLE t(id INT,v INT CONSTRAINT positive CHECK(v>0))", "WHEN MATCHED AND s.ord=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=-1", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (1, -1).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"updateThenDeleteCondition", "CREATE TABLE t(id INT,v INT)", "WHEN MATCHED AND s.ord=1 THEN UPDATE SET v=20 WHEN MATCHED AND 10/s.x>0 THEN DELETE", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT,x BIGINT)", 0))
				x := "0"
				if tc.name == "deleteThenRange" {
					x = "2147483648"
				}
				run(fixture.Exec("INSERT INTO source VALUES(0,2,1),(1,1,1),(2,1,"+x+")", 3))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id=s.id "+tc.arms+" WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)", tc.d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			})
		})
	}
}

func TestReferenceMergeDeleteAtomicity(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixedCount", true: "lateDivision"}[fail], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				run(fixture.Exec("CREATE TABLE source(id INT,x INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,1),(2,1),(4,1)", 3))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY id) s ON t.id=s.id WHEN MATCHED AND s.id=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40)"
				want := [][]any{{int64(2), int64(21)}, {int64(4), int64(40)}}
				if fail {
					q = "MERGE INTO t USING (SELECT * FROM source ORDER BY id) s ON t.id=s.id WHEN MATCHED AND s.id=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,10/(s.id-4))"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}
				} else {
					run(fixture.Exec(q, 4))
				}
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceMergeDeleteUniquePrefixes(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleteThenInsert", true: "insertThenDelete"}[reverse], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT CONSTRAINT unique_v UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT)", 0))
				seed := "(1,1),(2,2)"
				if reverse {
					seed = "(1,2),(2,1)"
				}
				run(fixture.Exec("INSERT INTO source VALUES"+seed, 2))
				// Avoid a hash-joinable equality: the prefix witness needs ordered
				// source candidates, not a target-driven hash join's output order.
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,10)"
				want := [][]any{{int64(2), int64(10)}}
				if reverse {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "unique_v"`, Detail: "Key (v)=(10) already exists.", Schema: "public", Table: "t", Constraint: "unique_v"})
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}}
				} else {
					run(fixture.Exec(q, 2))
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}
