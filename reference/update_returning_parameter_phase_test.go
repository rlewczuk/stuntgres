//go:build reference

package reference_test

import "testing"

func TestReferenceUpdateReturningParameterPhase(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,TRUE)", 1))
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"UPDATE t SET a=$1,f=$1 RETURNING 1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}},
		{"UPDATE t SET a=$1,f=$1 RETURNING $1::int4", referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: "21"}},
	} {
		p.step(t, referenceImplicitError(tc.q, tc.d, 2))
		p.step(t, referencePrepared(referenceImplicitError(tc.q, tc.d, 2)))
	}
	p.step(t, referenceSelect("SELECT a,f FROM t", []string{"a", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
}
