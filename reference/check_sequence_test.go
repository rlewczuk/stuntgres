//go:build reference

package reference_test

import "testing"

func TestReferenceCheckVolatileSequenceDependency(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(referenceExec("DROP SEQUENCE s CASCADE", 0))
		run(referenceExec("INSERT INTO t VALUES(3)", 1))
		run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}
