package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func mergeUpdateCardinalityDiagnostic() fixture.Diagnostic {
	return fixture.Diagnostic{Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
}

func TestReferenceMergeUpdateCardinality(t *testing.T) {
	for _, tc := range []struct {
		name, source, condition string
		n                       int64
		fail                    bool
	}{
		{"twice", "(1,1,TRUE),(2,1,TRUE)", "s.active", 0, true},
		{"trueFalse", "(1,1,TRUE),(2,1,FALSE)", "s.active", 1, false},
		{"falseFalse", "(1,1,FALSE),(2,1,FALSE)", "s.active", 0, false},
		{"sameValue", "(1,1,TRUE),(2,1,TRUE)", "TRUE", 0, true},
		{"mixedPrefix", "(0,2,TRUE),(1,1,TRUE),(2,1,TRUE)", "s.active", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT,active BOOL)", 0))
				count := int64(2)
				if tc.name == "mixedPrefix" {
					count = 3
				}
				run(fixture.Exec("INSERT INTO source VALUES"+tc.source, count))
				rhs := "t.v+1"
				if tc.name == "sameValue" {
					rhs = "t.v"
				}
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id=s.id WHEN MATCHED AND " + tc.condition + " THEN UPDATE SET v=" + rhs + " WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				if tc.fail {
					s := fixture.ImplicitError(q, mergeUpdateCardinalityDiagnostic())
					s.ErrorStage = "execute"
					run(s)
				} else {
					run(fixture.Exec(q, tc.n))
				}
				v := int64(10)
				if tc.n == 1 {
					v = 11
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), v}}))
			})
		})
	}
}

// These goldens are deliberately independent proposed release expectations.
// ORDER BY a physical source makes the second candidate/error unambiguous.
func TestReferenceMergeUpdateOraclePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, source, set string
		d                      fixture.Diagnostic
	}{
		{"secondDivision", "CREATE TABLE t(id INT,v INT)", "(1,1),(2,0)", "v=10/s.x", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"secondRange", "CREATE TABLE t(id INT,v INT)", "(1,1::bigint),(2,2147483648::bigint)", "v=s.x", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		{"secondNotNull", "CREATE TABLE t(id INT,v INT NOT NULL)", "(1,1),(2,NULL::bigint)", "v=s.x", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "t", Column: "v"}},
		{"secondCheck", "CREATE TABLE t(id INT,v INT CONSTRAINT positive CHECK(v>0))", "(1,1),(2,-1)", "v=s.x", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (1, -1).", Schema: "public", Table: "t", Constraint: "positive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,x BIGINT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES"+tc.source, 2))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON TRUE WHEN MATCHED THEN UPDATE SET "+tc.set, tc.d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			})
		})
	}
	for _, condition := range []string{"t.v=10", "t.v=20"} {
		t.Run(condition, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1),(2)", 2))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON TRUE WHEN MATCHED AND (s.ord=1 OR " + condition + ") THEN UPDATE SET v=20"
				// Each frozen join candidate evaluates WHEN against its original target row.
				if condition == "t.v=10" {
					s := fixture.ImplicitError(q, mergeUpdateCardinalityDiagnostic())
					s.ErrorStage = "execute"
					run(s)
				} else {
					run(fixture.Exec(q, 1))
				}
				v := int64(10)
				if condition == "t.v=20" {
					v = 20
				}
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{v}}))
			})
		})
	}
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE TABLE source(x INT)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1),(2)", 2))
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=(SELECT x FROM source)", fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"})
		s.ErrorStage = "execute"
		run(s)
	})
}

func TestReferenceMergeUpdateConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, set string
		d              fixture.Diagnostic
	}{
		{"division", "CREATE TABLE t(id INT,v INT)", "v=10/s.x", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"range", "CREATE TABLE t(id INT,v INT)", "v=CASE WHEN s.id=1 THEN 11::bigint ELSE 2147483648::bigint END", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
		{"notNull", "CREATE TABLE t(id INT,v INT NOT NULL)", "v=CASE WHEN s.id=1 THEN 11 ELSE NULL END", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "t", Column: "v"}},
		{"check", "CREATE TABLE t(id INT,v INT CONSTRAINT positive CHECK(v>0))", "v=CASE WHEN s.id=1 THEN 11 ELSE -1 END", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (2, -1).", Schema: "public", Table: "t", Constraint: "positive"}},
		{"unique", "CREATE TABLE t(id INT,v INT CONSTRAINT unique_v UNIQUE)", "v=11", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "unique_v"`, Detail: "Key (v)=(11) already exists.", Schema: "public", Table: "t", Constraint: "unique_v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				run(fixture.Exec("CREATE TABLE source(id INT,x INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,1),(2,0)", 2))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT * FROM source ORDER BY id) s ON t.id=s.id WHEN MATCHED THEN UPDATE SET "+tc.set, tc.d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT,g INT GENERATED ALWAYS AS(a+b) STORED)", 0))
		run(fixture.Exec("INSERT INTO t(id,a,b) VALUES(1,10,20)", 1))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET a=t.b+1,b=t.a,g=DEFAULT", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "a", "b", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(21), int64(10), int64(31)}}))
	})
}

func TestReferenceMergeUpdateAtomicity(t *testing.T) {
	for _, source := range []string{"(0,2,1),(1,1,0)", "(0,1,1),(1,2,0)"} {
		t.Run(source, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(ord INT,id INT,x INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES"+source, 2))
				s := fixture.ImplicitError("MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=10/s.x WHEN NOT MATCHED THEN INSERT VALUES(s.id,10/s.x)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			})
		})
	}
	for _, unique := range []bool{false, true} {
		t.Run(map[bool]string{false: "frozenBag", true: "frozenUnique"}[unique], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				ddl := "CREATE TABLE t(id INT,v INT)"
				if unique {
					ddl = "CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)"
				}
				run(fixture.Exec(ddl, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE TABLE source(id INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1),(2)", 2))
				q := "MERGE INTO t USING (SELECT * FROM source ORDER BY id) s ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=2 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				want := [][]any{{int64(2), int64(10)}, {int64(2), int64(99)}}
				if unique {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_key"`, Detail: "Key (id)=(2) already exists.", Schema: "public", Table: "t", Constraint: "t_key"})
					s.ErrorStage = "execute"
					run(s)
					want = [][]any{{int64(1), int64(10)}}
				} else {
					run(fixture.Exec(q, 2))
				}
				check := fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want)
				check.Bag = true
				run(check)
			})
		})
	}
}
