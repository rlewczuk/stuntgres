package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeNothingUnreachable(t *testing.T) {
	for _, kind := range []string{"MATCHED", "NOT MATCHED BY SOURCE", "NOT MATCHED", "NOT MATCHED BY TARGET"} {
		for _, condition := range []string{"", " AND FALSE"} {
			t.Run(kind+condition, func(t *testing.T) {
				mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeRun(t, p, b, prepared)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE s(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					mutation, other := "UPDATE SET v=99", "NOT MATCHED BY TARGET"
					if kind == "NOT MATCHED BY SOURCE" {
						mutation = "DELETE"
					}
					if kind == "NOT MATCHED" || kind == "NOT MATCHED BY TARGET" {
						mutation, other = "INSERT VALUES(s.id,s.v)", "MATCHED"
					}
					for _, first := range []string{"DO NOTHING", mutation} {
						for _, last := range []string{"DO NOTHING", mutation} {
							q := "MERGE INTO t USING s ON t.id=s.id WHEN " + kind + " THEN " + first + " WHEN " + other + " THEN DO NOTHING WHEN " + kind + condition + " THEN " + last
							mergeNothingAnalysisError(t, p, b, prepared, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"})
						}
					}
					run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
				})
			})
		}
	}
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE s(id INT,v INT)", 0))
		for _, q := range []string{
			"MERGE INTO t USING s ON t.missing=s.id WHEN MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE",
			"MERGE INTO t USING s ON TRUE WHEN MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE RETURNING t.missing",
			"MERGE INTO t USING s ON TRUE WHEN MATCHED AND t.missing=1 THEN DO NOTHING WHEN MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE",
			"MERGE INTO absent USING s ON TRUE WHEN MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE",
			"MERGE INTO t USING absent ON TRUE WHEN MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE",
			"MERGE INTO t USING s ON TRUE WHEN NOT MATCHED THEN DO NOTHING WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v)",
			"MERGE INTO t USING s ON TRUE WHEN NOT MATCHED BY TARGET THEN DO NOTHING WHEN NOT MATCHED THEN DO NOTHING",
		} {
			mergeNothingAnalysisError(t, p, b, prepared, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "unreachable WHEN clause specified after unconditional WHEN clause"})
		}
		run(fixture.Exec("MERGE INTO t USING s ON t.id=s.id WHEN MATCHED THEN DO NOTHING WHEN NOT MATCHED BY SOURCE THEN DO NOTHING WHEN NOT MATCHED THEN DO NOTHING", 0))
	})
}
func mergeNothingAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, q string, d fixture.Diagnostic) {
	t.Helper()
	s := fixture.ImplicitError(q, d)
	s.Prepared = prepared
	s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
	mergeReturningRun(t, p, b, s)
}
func TestReferenceMergeNothingNamespaces(t *testing.T) {
	for _, tc := range []struct {
		kind, condition, token string
		d                      fixture.Diagnostic
	}{
		{"NOT MATCHED BY SOURCE", "s.id=1", "s.id=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}},
		{"NOT MATCHED BY TARGET", "t.id=1", "t.id=1", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}},
		{"NOT MATCHED BY SOURCE", "(SELECT s.id)=1", "s.id)", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "s"`, Detail: `There is an entry for table "s", but it cannot be referenced from this part of the query.`}},
		{"NOT MATCHED BY TARGET", "(SELECT t.id)=1", "t.id)", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "t"`, Detail: `There is an entry for table "t", but it cannot be referenced from this part of the query.`}},
		{"MATCHED", "id=1", "id=1", fixture.Diagnostic{Code: "42702", Message: `column reference "id" is ambiguous`}},
		{"MATCHED", "missing=1", "missing=1", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`}},
		{"MATCHED", "1", "1 THEN", fixture.Diagnostic{Code: "42804", Message: "argument of WHEN must be type boolean, not type integer"}},
	} {
		t.Run(tc.kind+tc.condition, func(t *testing.T) {
			mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("CREATE TABLE s(id INT,v INT)", 0))
				q := "/*é*/ MERGE INTO t USING s ON t.id=s.id WHEN " + tc.kind + " AND " + tc.condition + " THEN DO NOTHING"
				d := mergeReturningDiagnostic(q, tc.token, tc.d.Code, tc.d.Message)
				d.Detail, d.Hint = tc.d.Detail, tc.d.Hint
				mergeNothingAnalysisError(t, p, b, prepared, q, d)
			})
		})
	}
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE s(id INT)", 0))
		for _, tc := range []struct{ kind, column, table string }{
			{"NOT MATCHED BY SOURCE", "source_only", "src"},
			{"NOT MATCHED BY TARGET", "v", "t"},
		} {
			q := "/*é*/ MERGE INTO t USING (SELECT id AS source_only FROM s) src ON TRUE WHEN " + tc.kind + " AND " + tc.column + "=1 THEN DO NOTHING"
			d := mergeReturningDiagnostic(q, tc.column+"=1", "42703", `column "`+tc.column+`" does not exist`)
			d.Detail = `There is a column named "` + tc.column + `" in table "` + tc.table + `", but it cannot be referenced from this part of the query.`
			mergeNothingAnalysisError(t, p, b, prepared, q, d)
		}
		q := "/*é*/ MERGE INTO t USING s ON TRUE WHEN MATCHED AND (SELECT id FROM absent)=1 THEN DO NOTHING"
		mergeNothingAnalysisError(t, p, b, prepared, q, mergeReturningDiagnostic(q, "absent", "42P01", `relation "absent" does not exist`))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
		run(fixture.Exec("MERGE INTO t USING s ON t.id=s.id WHEN MATCHED AND (SELECT s.id)=1 AND (SELECT t.v)=10 THEN DO NOTHING", 0))
		run(fixture.Exec("DELETE FROM s", 1))
		run(fixture.Exec("MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND (SELECT t.v)=10 THEN DO NOTHING", 0))
		run(fixture.Exec("DELETE FROM t", 1))
		run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
		run(fixture.Exec("MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED AND (SELECT s.id)=1 THEN DO NOTHING", 0))
	})
}
