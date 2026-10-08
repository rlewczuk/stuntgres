package reference_test

import "testing"

func TestReferenceInsertSelectStarDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		source, q string
		want      referenceDiagnostic
	}{
		{"CREATE TABLE s(a INT,b INT)", "INSERT INTO t(i) SELECT * FROM s", referenceDiagnostic{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t(i) SELECT * FROM s", referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "/*é*/ INSERT INTO t(i) SELECT * FROM s", referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "31"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t SELECT *", referenceDiagnostic{Code: "42601", Message: "SELECT * with no tables specified is not valid", Position: "22"}},
	} {
		t.Run(tc.q+tc.source, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(i INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(9)", 1))
					run(referenceExec(tc.source, 0))
					for _, prepared := range []bool{false, true} {
						failure := referenceImplicitError(tc.q, tc.want)
						failure.prepared, failure.errorStage = prepared, "execute"
						if prepared {
							failure.errorStage = "prepare"
						}
						run(failure)
						run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					}
				})
			}
		})
	}
}
