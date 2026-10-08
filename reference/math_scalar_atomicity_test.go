//go:build reference

package reference_test

import "testing"

func TestReferenceMathScalarDomainAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE source(id INT,s SMALLINT,b BIGINT,n NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO source VALUES(1,-5,5,7.00),(2,-32768,-1,8.00)", 2))
	p.step(t, referenceExec("CREATE TABLE copied(n NUMERIC)", 0))
	for _, tc := range []struct{ query, message string }{
		{"UPDATE source SET n=abs(s)", "smallint out of range"},
		{"INSERT INTO copied SELECT abs(s) FROM source", "smallint out of range"},
		{"UPDATE source SET n=factorial(b)", "factorial of a negative number is undefined"},
		{"INSERT INTO copied SELECT factorial(b) FROM source", "factorial of a negative number is undefined"},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22003", Message: tc.message})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
			p.step(t, referenceSelect("SELECT n FROM source ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"7.00"}, {"8.00"}}))
			p.step(t, referenceSelect("SELECT n FROM copied", []string{"n"}, []string{"NUMERIC"}, [][]any{}))
		}
	}
}
