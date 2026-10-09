package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceCheckVolatileSequenceDependency(t *testing.T) {
	for _, cascadePrepared := range []bool{false, true} {
		t.Run(fmt.Sprint(cascadePrepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					run(referenceExec("CREATE SEQUENCE s START 1", 0))
					run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
					if !b.pg {
						referenceCheckDump(t, p)
					}
					run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
					if !b.pg {
						referenceCheckRoundtrip(t, p, referenceCheckDump(t, p))
					}
					run(referenceExec("INSERT INTO t VALUES(3)", 1))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(3)}}))
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
						s.prepared = prepared
						run(s)
					}
					run(referenceExec("INSERT INTO t VALUES(4)", 1))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(4)}}))
					s := referenceExec("DROP SEQUENCE s CASCADE", 0)
					s.prepared = cascadePrepared
					run(s)
					run(referenceExec("INSERT INTO t VALUES(5)", 1))
					run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
				})
			}
		})
	}
}

func TestReferenceCheckSequenceDropTransactionalCandidates(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
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
		})
	}
}
