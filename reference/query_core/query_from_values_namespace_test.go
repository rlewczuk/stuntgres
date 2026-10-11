package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryFromValuesNamespaceDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  fixture.Diagnostic
	}{
		{"SELECT v.nn FROM (VALUES(1)) v(n)", fixture.Diagnostic{Code: "42703", Message: "column v.nn does not exist", Hint: `Perhaps you meant to reference the column "v.n".`, Position: "8"}},
		{"SELECT * FROM (VALUES(1)) a(n), (VALUES(a.n)) b(n)", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`, Hint: "To reference that table, you must mark this subquery with LATERAL.", Position: "41"}},
		{"SELECT * FROM t, (VALUES(n)) v(m)", fixture.Diagnostic{Code: "42703", Message: `column "n" does not exist`, Detail: `There is a column named "n" in table "t", but it cannot be referenced from this part of the query.`, Hint: "To reference that column, you must mark this subquery with LATERAL.", Position: "26"}},
		{"SELECT * FROM (VALUES(1)) a(n), (VALUES(n)) b(m)", fixture.Diagnostic{Code: "42703", Message: `column "n" does not exist`, Detail: `There is a column named "n" in table "a", but it cannot be referenced from this part of the query.`, Hint: "To reference that column, you must mark this subquery with LATERAL.", Position: "41"}},
		{"SELECT nn FROM (VALUES(1)) v(n)", fixture.Diagnostic{Code: "42703", Message: `column "nn" does not exist`, Hint: `Perhaps you meant to reference the column "v.n".`, Position: "8"}},
		{"SELECT column11 FROM (VALUES(1))", fixture.Diagnostic{Code: "42703", Message: `column "column11" does not exist`, Hint: `Perhaps you meant to reference the column "unnamed_subquery.column1".`, Position: "8"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				for _, prepared := range []bool{false, true} {
					for _, queryRows := range []bool{false, true} {
						s := fixture.ImplicitError(tc.query, tc.want)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryRows, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
					}
				}
			}
		})
	}
}
