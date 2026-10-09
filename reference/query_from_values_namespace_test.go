package reference_test

import "testing"

func TestReferenceQueryFromValuesNamespaceDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  referenceDiagnostic
	}{
		{"SELECT v.nn FROM (VALUES(1)) v(n)", referenceDiagnostic{Code: "42703", Message: "column v.nn does not exist", Hint: `Perhaps you meant to reference the column "v.n".`, Position: "8"}},
		{"SELECT * FROM (VALUES(1)) a(n), (VALUES(a.n)) b(n)", referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`, Hint: "To reference that table, you must mark this subquery with LATERAL.", Position: "41"}},
		{"SELECT * FROM t, (VALUES(n)) v(m)", referenceDiagnostic{Code: "42703", Message: `column "n" does not exist`, Detail: `There is a column named "n" in table "t", but it cannot be referenced from this part of the query.`, Hint: "To reference that column, you must mark this subquery with LATERAL.", Position: "26"}},
		{"SELECT * FROM (VALUES(1)) a(n), (VALUES(n)) b(m)", referenceDiagnostic{Code: "42703", Message: `column "n" does not exist`, Detail: `There is a column named "n" in table "a", but it cannot be referenced from this part of the query.`, Hint: "To reference that column, you must mark this subquery with LATERAL.", Position: "41"}},
		{"SELECT nn FROM (VALUES(1)) v(n)", referenceDiagnostic{Code: "42703", Message: `column "nn" does not exist`, Hint: `Perhaps you meant to reference the column "v.n".`, Position: "8"}},
		{"SELECT column11 FROM (VALUES(1))", referenceDiagnostic{Code: "42703", Message: `column "column11" does not exist`, Hint: `Perhaps you meant to reference the column "unnamed_subquery.column1".`, Position: "8"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(n INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				for _, prepared := range []bool{false, true} {
					for _, queryRows := range []bool{false, true} {
						s := referenceImplicitError(tc.query, tc.want)
						s.prepared, s.selectRows, s.errorStage = prepared, queryRows, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
						run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
					}
				}
			}
		})
	}
}
