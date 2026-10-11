package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticParameterDiagnostics(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(i INT,b BIGINT,s TEXT)", 0))
			if !empty {
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,7,'x')", 1))
			}
			for _, tc := range []struct {
				q    string
				d    fixture.Diagnostic
				args []any
			}{{"SELECT /*é*/ i+TRUE FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil}, {"SELECT $1,i+$1 FROM t", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{7}}} {
				step := fixture.ImplicitError(tc.q, tc.d, tc.args...)
				step.SelectRows = true
				step.ErrorStage = "execute"
				p.Step(t, step)
				step.ErrorStage = "prepare"
				p.Step(t, fixture.Prepared(step))
			}
			step := fixture.ImplicitError("SELECT i+$1 FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, "oops")
			step.SelectRows = true
			step.ErrorStage = "execute"
			pg := *step.Diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.Diagnostic = &pg
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
}

func TestReferenceArithmeticSignedNumericLiteral(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(v NUMERIC)", 0))
	p.Step(t, fixture.Exec("INSERT INTO n VALUES(+1.5)", 1))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO n VALUES(+9223372036854775808)", 1)))
	p.Step(t, fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}})))
}
