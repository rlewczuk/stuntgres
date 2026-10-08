//go:build reference

package reference_test

import "testing"

// SQL controls only: PostgreSQL has no stuntgres native replacement API.
func TestReferenceCheckReviewReplayHistory(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))", 0))
		run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("INSERT INTO t VALUES(9)", 1))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceExec("CREATE SEQUENCE replay_s START 1", 0))
		run(referenceExec("CREATE TABLE replay_t(a INT CONSTRAINT c CHECK(nextval('replay_s')=2))", 0))
		run(referenceImplicitError("INSERT INTO replay_t VALUES(9)", referenceCheckViolation("replay_t", "c", "9")))
		run(referenceSelect("SELECT a FROM replay_t", []string{"a"}, []string{"INT4"}, [][]any{}))
		run(referenceSelect("SELECT currval('replay_s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}

func TestReferenceCheckReviewReplayOwnedSequence(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
		run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
		run(referenceExec("ALTER SEQUENCE s OWNED BY t.a", 0))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES($1)", 1, 11)))
		run(referenceExec("INSERT INTO t VALUES(22)", 1))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(referenceSelect("SELECT setval('s',-1)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(-1)}}))
		run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "33"), 33)))
		run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(22)}}))
		run(referenceExec("DROP TABLE t", 0))
		run(referenceImplicitError("SELECT nextval('s')", referenceDiagnostic{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}))
	})
}
