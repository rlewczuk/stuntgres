package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceMergeUpdateBindings(t *testing.T) {
	mergeUpdateAnalysisCases(t, []mergeUpdateAnalysisCase{
		{"ambiguousOn", "MERGE INTO t USING s ON id=s.id WHEN MATCHED THEN UPDATE SET a=1", "id=s", fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"ambiguousWhen", "MERGE INTO t USING s ON TRUE WHEN MATCHED AND id=1 THEN UPDATE SET a=1", "id=1", fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"ambiguousSet", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=id", "id", fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"hiddenTarget", "MERGE INTO t dst USING s src ON dst.id=src.id WHEN MATCHED THEN UPDATE SET a=t.a", "t.a", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Hint: `Perhaps you meant to reference the table alias "dst".`}},
		{"hiddenSource", "MERGE INTO t dst USING s src ON dst.id=src.id WHEN MATCHED THEN UPDATE SET a=s.id", "s.id", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Hint: `Perhaps you meant to reference the table alias "src".`}},
		{"unmatchedHiddenTarget", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id,t.a)", "t.a", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}},
	})
}

type mergeUpdateAnalysisCase struct {
	name, q, token string
	d              fixture.Diagnostic
}

func mergeUpdateAnalysisCases(t *testing.T, cases []mergeUpdateAnalysisCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
				run(fixture.Exec("CREATE TABLE s(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(99,88)", 1))
				q := "/* é */ " + tc.q
				d := tc.d
				if tc.token != "" {
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1)
				}
				s := fixture.ImplicitError(q, d)
				s.Prepared = prepared
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				// Query must reject the same analysis with no successful cursor.
				s.SelectRows = true
				s.Prepared = false
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(88)}}))
			})
		})
	}
}

func TestReferenceMergeUpdateDiagnostics(t *testing.T) {
	mergeUpdateAnalysisCases(t, []mergeUpdateAnalysisCase{
		{"missingTarget", "MERGE INTO absent USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=1", "absent", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
		{"missingSource", "MERGE INTO t USING absent ON TRUE WHEN MATCHED THEN UPDATE SET a=1", "absent", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}},
		{"missingDestination", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET missing=1", "missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
		{"duplicate", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=1,a=2", "", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"plainRHS", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=TRUE", "TRUE", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"groupedRHS", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=(TRUE)", "TRUE", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"arithmeticRHS", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=s.id::bigint>0", "s.id::bigint", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
		{"whenType", "MERGE INTO t USING s ON TRUE WHEN MATCHED AND 1 THEN UPDATE SET a=1", "1 THEN", fixture.Diagnostic{Code: "42804", Message: "argument of WHEN must be type boolean, not type integer"}},
		{"unreachable", "MERGE INTO t USING s ON TRUE WHEN MATCHED THEN UPDATE SET a=1 WHEN MATCHED AND FALSE THEN UPDATE SET a=2", "", fixture.Diagnostic{Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"}},
	})
}

func TestReferenceMergeUpdateAssignmentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, ddl, seed, set, token string
		d                           fixture.Diagnostic
		pgDefault                   bool
	}{
		{"ordinaryDefault", "CREATE TABLE t(id INT,a INT DEFAULT 7)", "INSERT INTO t VALUES(1,10)", "a=DEFAULT", "DEFAULT", fixture.Diagnostic{Code: "0A000", Message: "UPDATE DEFAULT is unsupported"}, true},
		{"arrayDefault", "CREATE TABLE t(id INT,a INT[])", "INSERT INTO t VALUES(1,ARRAY[10])", "a[1]=DEFAULT", "a[1]", fixture.Diagnostic{Code: "0A000", Message: "cannot set an array element to DEFAULT"}, false},
		{"generated", "CREATE TABLE t(id INT,a INT GENERATED ALWAYS AS(id+1) STORED)", "INSERT INTO t(id) VALUES(1)", "a=7", "", fixture.Diagnostic{Code: "428C9", Message: `column "a" can only be updated to DEFAULT`, Detail: `Column "a" is a generated column.`}, false},
		{"identity", "CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY,a INT)", "INSERT INTO t(a) VALUES(10)", "id=7", "", fixture.Diagnostic{Code: "428C9", Message: `column "id" can only be updated to DEFAULT`, Detail: `Column "id" is an identity column defined as GENERATED ALWAYS.`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec(tc.ddl, 0))
				run(fixture.Exec(tc.seed, 1))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET " + tc.set
				if tc.pgDefault && b.PG {
					run(fixture.Exec(q, 1))
					run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					return
				}
				d := tc.d
				if tc.token != "" {
					d.Position = fixture.Position(strings.LastIndex(q, tc.token) + 1)
				}
				s := fixture.ImplicitError(q, d)
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
			})
		})
	}
}
