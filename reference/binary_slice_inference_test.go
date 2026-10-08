//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceBinarySliceInference(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT overlay(v,$1,$1) FROM t", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus integer", Position: "21"}},
		{"SELECT substr(v) FROM t", referenceDiagnostic{Code: "42883", Message: "function substr(bytea) does not exist", Hint: hint, Position: "8"}},
		{"SELECT overlay(v,s,1,2,3) FROM t", referenceDiagnostic{Code: "42883", Message: "function overlay(bytea, bytea, integer, integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT missing.substr(v,1) FROM t", referenceDiagnostic{Code: "3F000", Message: `schema "missing" does not exist`, Position: "8"}},
		{"SELECT substr(v," + strings.Repeat("1,", 99) + "1) FROM t", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{`SELECT substring(NULL::bytea FROM 'bad') FROM t`, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "35"}},
	} {
		step := referenceImplicitError(tc.q, tc.want)
		step.selectRows = true
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("INSERT INTO t VALUES('abcdef','XY')", 1))
	success := referenceSelect("SELECT substr(v,VARIADIC 1) AS v FROM t", []string{"v"}, []string{"BYTEA"}, [][]any{{"abcdef"}})
	p.step(t, success)
	p.step(t, referencePrepared(success))
}
