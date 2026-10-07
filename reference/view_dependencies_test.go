//go:build reference

package reference_test

import "testing"

func TestReferenceViewNestedDependencies(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT)", 0),
		referenceExec("CREATE VIEW v AS SELECT id FROM t", 0),
		referenceExec("CREATE VIEW w AS SELECT id FROM v", 0),
		referenceImplicitError("DROP TABLE t RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop table t because other objects depend on it", Detail: "view v depends on table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
		referenceExec("CREATE SEQUENCE s", 0),
		referenceExec("CREATE VIEW sv AS SELECT last_value FROM s", 0),
		referenceExec("CREATE VIEW sw AS SELECT last_value FROM sv", 0),
		referenceImplicitError("DROP SEQUENCE s RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "view sv depends on sequence s\nview sw depends on view sv", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
		referenceExec("DROP TABLE t CASCADE", 0),
		referenceExec("DROP SEQUENCE s CASCADE", 0),
	} {
		p.step(t, step)
	}
}
