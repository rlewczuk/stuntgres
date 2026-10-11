package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdateBindings(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE source(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(2,10)", 1))
		run(fixture.Exec("INSERT INTO source VALUES(1,7)", 1))
		// A shared unqualified name resolves to the visible target, not USING.
		run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND v=10 THEN UPDATE SET v=v+1", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(11)}}))
		// USING is hidden, not the physical relation or a locally introduced alias.
		run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT max(v) FROM source independent)+t.v", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(18)}}))
		run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=(SELECT s.v FROM source s WHERE s.id=1)", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}}))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		// Leaving a target-only namespace restores USING for later MATCHED arms.
		run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=v+1 WHEN MATCHED THEN UPDATE SET v=s.v+1", 2))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), int64(8)}}))
	})
}

func TestReferenceMergeSourceUpdateDiagnostics(t *testing.T) {
	mergeUpdateAnalysisCases(t, []mergeUpdateAnalysisCase{
		{"duplicate", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=1,a=2", "", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"unreachable", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=1 WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET a=2", "", fixture.Diagnostic{Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"}},
		{"whenType", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE AND 1 THEN UPDATE SET a=1", "1 THEN", fixture.Diagnostic{Code: "42804", Message: "argument of WHEN must be type boolean, not type integer"}},
		{"rhsType", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=TRUE", "TRUE", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"missingDestination", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE THEN UPDATE SET missing=1", "missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
		{"sourceWhen", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE AND s.id=1 THEN UPDATE SET a=1", "s.id", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}},
		{"sourceRHS", "MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET a=s.id", "s.id", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}},
	})
}

func TestReferenceMergeSourceUpdateAssignments(t *testing.T) {
	// Target-correlated scalar array bounds disconnect PostgreSQL18.0.
	// Direct target bounds and independent scalar bounds cover the supported
	// release paths; scalar RHS correlation is checked separately.
	for _, tc := range []struct {
		name, ddl, seed, set string
		args                 []any
		names, types         []string
		rows                 [][]any
		array                bool
	}{
		{"parametersOriginalFrame", "CREATE TABLE t(id INT,a INT,b INT,note TEXT,flag BOOL,w BIGINT,data BYTEA)", "INSERT INTO t VALUES(2,10,20,'old',TRUE,9000000000,'\\x0102')", "a=t.b+$1::integer,b=t.a,note=$2::text,flag=$3::boolean,w=t.w+1,data=$4::bytea", []any{3, nil, false, []byte{3, 4}}, []string{"id", "a", "b", "note", "flag", "w", "data"}, []string{"INT4", "INT4", "INT4", "TEXT", "BOOL", "INT8", "BYTEA"}, [][]any{{int64(2), int64(23), int64(10), nil, false, int64(9000000001), "\x03\x04"}}, false},
		{"scalarTarget", "CREATE TABLE t(id INT,a INT,b INT)", "INSERT INTO t VALUES(2,10,20)", "a=(SELECT t.b+$1::integer),b=t.a", []any{3}, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(23), int64(10)}}, false},
		{"generatedDefault", "CREATE TABLE t(id INT,a INT,b INT,g INT GENERATED ALWAYS AS(a+b) STORED)", "INSERT INTO t(id,a,b) VALUES(2,10,20)", "a=t.b+$1::integer,b=t.a,g=DEFAULT", []any{3}, []string{"id", "a", "b", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(23), int64(10), int64(33)}}, false},
		{"scalarGeneratedRetained", "CREATE TABLE t(id INT,a INT,b INT,g INT GENERATED ALWAYS AS(a+b) STORED)", "INSERT INTO t(id,a,b) VALUES(2,10,20)", "a=(SELECT t.b+$1::integer),b=t.a", []any{3}, []string{"id", "a", "b", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(23), int64(10), int64(33)}}, false},
		{"scalarGeneratedDefault", "CREATE TABLE t(id INT,a INT,b INT,g INT GENERATED ALWAYS AS(a+b) STORED)", "INSERT INTO t(id,a,b) VALUES(2,10,20)", "a=(SELECT t.b+$1::integer),b=t.a,g=DEFAULT", []any{3}, []string{"id", "a", "b", "g"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(23), int64(10), int64(33)}}, false},
		{"arrayOldFrame", "CREATE TABLE t(id INT,a INT[])", "INSERT INTO t VALUES(2,ARRAY[10,20,30])", "a[t.id]=t.a[1],a[1]=t.a[t.id]", nil, []string{"a"}, []string{"_INT4"}, [][]any{{"{20,10,30}"}}, true},
		{"arrayScalarBound", "CREATE TABLE t(id INT,a INT[])", "INSERT INTO t VALUES(2,ARRAY[10,20,30])", "a[(SELECT 2)]=t.a[1],a[1]=t.a[t.id]", nil, []string{"a"}, []string{"_INT4"}, [][]any{{"{20,10,30}"}}, true},
		{"arrayOverlap", "CREATE TABLE t(id INT,a INT[])", "INSERT INTO t VALUES(2,ARRAY[10,20,30])", "a[1:2]=ARRAY[7,8],a[2:3]=ARRAY[9,10]", nil, []string{"a"}, []string{"_INT4"}, [][]any{{"{7,9,10}"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec(tc.seed, 1))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET " + tc.set
				run(fixture.Exec(q, 1, tc.args...))
				if tc.array {
					run(fixture.ArrayReferenceSelect("SELECT a FROM t", tc.names, tc.types, tc.types, tc.rows, b.PG))
				} else {
					run(fixture.Select("SELECT * FROM t", tc.names, tc.types, tc.rows))
				}
			})
		})
	}
}

func TestReferenceMergeSourceUpdateJoinLegality(t *testing.T) {
	for _, tc := range []struct {
		name, on string
		insert   bool
		count    int64
		rows     [][]any
	}{
		{"singleClassInequality", "t.id<s.id", false, 1, [][]any{{int64(1), int64(10)}, {int64(2), int64(120)}}},
		{"equalityResidual", "t.id=s.id AND t.v>0", true, 1, [][]any{{int64(1), int64(110)}, {int64(2), int64(20)}}},
		{"falseConstant", "FALSE", true, 3, [][]any{{int64(1), int64(110)}, {int64(2), int64(120)}, {int64(2), int64(7)}}},
		{"trueConstant", "TRUE", true, 0, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				q := "MERGE INTO t USING (VALUES(2,7)) s(id,x) ON " + tc.on + " WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"
				if tc.insert {
					q += " WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.x)"
				}
				run(fixture.Exec(q, tc.count))
				check := fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows)
				check.Bag = true
				run(check)
			})
		})
	}
}
