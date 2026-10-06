//go:build reference

package reference_test

import "testing"

func TestReferenceInsertSelectZeroWidth(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT)", 0), referenceExec("INSERT INTO s VALUES(1),(1),(2)", 3), referenceExec("CREATE TABLE t(x INT)", 0), referenceExec("INSERT INTO t SELECT FROM s WHERE i=1", 2), referencePrepared(referenceExec("INSERT INTO t (SELECT)", 1))} {
		p.step(t, s)
	}
	p.step(t, referenceSelect("SELECT x FROM t", []string{"x"}, []string{"INT4"}, [][]any{{nil}, {nil}, {nil}}))
	for _, prepared := range []bool{false, true} {
		failure := referenceImplicitError("INSERT INTO t(x) SELECT", referenceDiagnostic{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "15"})
		failure.prepared = prepared
		failure.errorStage = "execute"
		if prepared {
			failure.errorStage = "prepare"
		}
		p.step(t, failure)
	}
	p.step(t, referenceSelect("SELECT x FROM t", []string{"x"}, []string{"INT4"}, [][]any{{nil}, {nil}, {nil}}))
}
