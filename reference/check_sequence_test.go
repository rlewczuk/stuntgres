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
		run(referencePrepared(referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})))
		run(referenceExec("INSERT INTO t VALUES(3)", 1))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referencePrepared(referenceExec("DROP SEQUENCE s CASCADE", 0)))
		run(referenceExec("INSERT INTO t VALUES(4)", 1))
		run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
	})
}

func TestReferenceCheckSequenceDropTransactionalCandidates(t *testing.T) {
	scenario := func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SAVEPOINT before_drop", 0))
		run(referenceExec("DROP SEQUENCE s CASCADE", 0))
		run(referenceExec("INSERT INTO t VALUES(2)", 1))
		run(referenceExec("ROLLBACK TO before_drop", 0))
		run(referenceExec("INSERT INTO t VALUES(3)", 1))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceExec("ROLLBACK", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referencePrepared(referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("DROP SEQUENCE s CASCADE", 0))
		run(referenceExec("INSERT INTO t VALUES(4)", 1))
		run(referenceExec("COMMIT", 0))
		run(referenceExec("INSERT INTO t VALUES(5)", 1))
		run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(5)}}))
	}
	p := newReferencePair(t)
	for _, postgres := range []bool{true, false} {
		name := "memory"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			t.Cleanup(func() { conn.ExecContext(p.ctx, "ROLLBACK") })
			scenario(t, func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) })
		})
	}
}
