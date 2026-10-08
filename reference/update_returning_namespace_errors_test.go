//go:build reference

package reference_test

import "testing"

func TestReferenceUpdateReturningNamespaceErrors(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1)", 1))
	p.step(t, referenceImplicitError("UPDATE t SET id=2 RETURNING id AS label,label", referenceDiagnostic{Code: "42703", Message: `column "label" does not exist`, Position: "41"}))
	p.step(t, referenceExec("ALTER TABLE t DROP COLUMN id", 0))
	p.step(t, referencePrepared(referenceImplicitError("UPDATE t SET absent=1 RETURNING *", referenceDiagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: "33"})))
}
