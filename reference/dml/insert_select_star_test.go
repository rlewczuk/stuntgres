package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectStarDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		source, q string
		want      fixture.Diagnostic
	}{
		{"CREATE TABLE s(a INT,b INT)", "INSERT INTO t(i) SELECT * FROM s", fixture.Diagnostic{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t(i) SELECT * FROM s", fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "25"}},
		{"CREATE TABLE s(v TEXT)", "/*é*/ INSERT INTO t(i) SELECT * FROM s", fixture.Diagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "31"}},
		{"CREATE TABLE s(v TEXT)", "INSERT INTO t SELECT *", fixture.Diagnostic{Code: "42601", Message: "SELECT * with no tables specified is not valid", Position: "22"}},
	} {
		t.Run(tc.q+tc.source, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(i INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
					run(fixture.Exec(tc.source, 0))
					for _, prepared := range []bool{false, true} {
						failure := fixture.ImplicitError(tc.q, tc.want)
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						if prepared {
							failure.ErrorStage = "prepare"
						}
						run(failure)
						run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					}
				})
			}
		})
	}
}
