//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceCheckAlterCandidates(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceCheckBackends(t, func(t *testing.T, step func(referenceStep)) {
				run := func(s referenceStep) { s.prepared = prepared; step(s) }
				run(referenceExec("CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(9,1,NULL)", 1))
				run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT c,ALTER b SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"}))
				run(referenceImplicitError("INSERT INTO t VALUES(9,2,NULL)", referenceCheckViolation("t", "c", "9, 2, null")))
				run(referenceSelect("SELECT * FROM t", []string{"p", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), nil}}))
				run(referenceExec("ALTER TABLE t DROP p,DROP b", 0))
				run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("ALTER TABLE t DROP CONSTRAINT c", 0))
				run(referenceExec("INSERT INTO t VALUES(2)", 1))
				run(referenceExec("ROLLBACK", 0))
				run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SAVEPOINT keep_check", 0))
				run(referenceExec("ALTER TABLE t DROP CONSTRAINT c", 0))
				run(referenceExec("INSERT INTO t VALUES(2)", 1))
				run(referenceExec("ROLLBACK TO keep_check", 0))
				run(referenceExec("ALTER TABLE t DROP CONSTRAINT c", 0))
				run(referenceExec("INSERT INTO t VALUES(2)", 1))
				run(referenceExec("COMMIT", 0))
				run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(referenceExec("CREATE SEQUENCE s", 0))
				run(referenceExec("CREATE TABLE u(p INT,a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
				run(referenceExec("ALTER SEQUENCE s OWNED BY u.p", 0))
				run(referenceExec("ALTER TABLE u DROP CONSTRAINT c,DROP p", 0))
				run(referenceExec("INSERT INTO u VALUES(2)", 1))
				run(referenceSelect("SELECT * FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
			})
		})
	}
}

func TestReferenceCheckAlterOwnedSequenceAutoConstraint(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s", 0))
		run(referenceExec("CREATE TABLE t(p INT,a INT,CONSTRAINT c CHECK(p>0 AND nextval('s')>0))", 0))
		run(referenceExec("ALTER SEQUENCE s OWNED BY t.p", 0))
		run(referenceExec("ALTER TABLE t DROP p", 0))
		run(referenceExec("INSERT INTO t VALUES(2)", 1))
		run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}
