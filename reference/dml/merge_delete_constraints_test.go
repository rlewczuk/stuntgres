package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, last string
		d               fixture.Diagnostic
	}{
		{"lateCondition", "CREATE TABLE t(id INT,v INT)", "WHEN MATCHED AND s.ord=4 AND 10/s.x>0 THEN DELETE WHEN MATCHED THEN UPDATE SET v=21", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"lateNotNull", "CREATE TABLE t(id INT,v INT NOT NULL)", "WHEN MATCHED THEN UPDATE SET v=CASE WHEN s.ord=4 THEN NULL ELSE 21 END", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "t", Column: "v"}},
		{"lateCheck", "CREATE TABLE t(id INT,v INT CONSTRAINT positive CHECK(v>0))", "WHEN MATCHED THEN UPDATE SET v=CASE WHEN s.ord=4 THEN -1 ELSE 21 END", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (3, -1).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"lateUnique", "CREATE TABLE t(id INT,v INT CONSTRAINT unique_v UNIQUE)", "WHEN MATCHED THEN UPDATE SET v=21", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "unique_v"`, Detail: "Key (v)=(21) already exists.", Schema: "public", Table: "t", Constraint: "unique_v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT,x INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,1,1),(2,2,1),(3,4,1),(4,3,0)", 4))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED AND s.id=1 THEN DELETE " + tc.last + " WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)"
				s := fixture.ImplicitError(q, tc.d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
			})
		})
	}
}

func TestReferenceMergeDeleteExclusionPrefixes(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleteThenInsert", true: "insertThenDelete"}[reverse], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT,CONSTRAINT ex EXCLUDE USING btree(v WITH =))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT)", 0))
				seed := "(1,1),(2,2)"
				if reverse {
					seed = "(1,2),(2,1)"
				}
				run(fixture.Exec("INSERT INTO source VALUES"+seed, 2))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,10)"
				want := [][]any{{int64(2), int64(10)}}
				if reverse {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23P01", Message: `conflicting key value violates exclusion constraint "ex"`, Detail: "Key (v)=(10) conflicts with existing key (v)=(10).", Schema: "public", Table: "t", Constraint: "ex"})
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
