//go:build reference

package reference_test

import "testing"

func TestReferenceLogicalConcreteTypes(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,n NUMERIC,tm TIME)", 0))
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"SELECT TRUE OR n FROM t", referenceDiagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type numeric", Position: "16"}},
		{"SELECT NOT tm FROM t", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type time without time zone", Position: "12"}},
	} {
		s := referenceImplicitError(tc.q, tc.d)
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
}
