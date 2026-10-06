//go:build reference

package reference_test

import "testing"

func TestReferenceArithmeticParameterDiagnostics(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT,b BIGINT,s TEXT)", 0))
			if !empty {
				p.step(t, referenceExec("INSERT INTO t VALUES(7,7,'x')", 1))
			}
			for _, tc := range []struct {
				q    string
				d    referenceDiagnostic
				args []any
			}{{"SELECT /*é*/ i+TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil}, {"SELECT $1,i+$1 FROM t", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{7}}} {
				step := referenceImplicitError(tc.q, tc.d, tc.args...)
				step.selectRows = true
				step.errorStage = "execute"
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
			}
			step := referenceImplicitError("SELECT i+$1 FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, "oops")
			step.selectRows = true
			step.errorStage = "execute"
			pg := *step.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.postgresDiagnostic = &pg
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
}

func TestReferenceArithmeticSignedNumericLiteral(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE n(v NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO n VALUES(+1.5)", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO n VALUES(+9223372036854775808)", 1)))
	p.step(t, referenceSelect("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.5"}, {"9223372036854775808"}}))
}
