package reference_test

import (
	"bytes"
	"fmt"
	"testing"
)

func referenceCheckReview2OrderError(detail string) referenceDiagnostic {
	return referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func TestReferenceCheckReview2OrderReverseMixed(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					exec := func(q string, count int64) {
						s := referenceExec(q, count)
						s.prepared = prepared
						run(s)
					}
					exec("CREATE SEQUENCE s", 0)
					exec("CREATE VIEW v AS SELECT nextval('s') AS n", 0)
					exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0)
					exec("INSERT INTO t VALUES(7)", 1)
					var before []byte
					if !b.pg {
						before = referenceCheckDump(t, p)
					}
					for _, dropPrepared := range []bool{false, true} {
						s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("view v depends on sequence s\nconstraint c on table t depends on sequence s"))
						s.prepared = dropPrepared
						run(s)
					}
					if !b.pg && !bytes.Equal(before, referenceCheckDump(t, p)) {
						t.Fatal("RESTRICT changed catalog/provider state")
					}
					run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(referenceSelect("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					exec("INSERT INTO t VALUES(8)", 1)
					run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
				})
			}
		})
	}
}

func TestReferenceCheckReview2OrderMultipleOwners(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					run(referenceExec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
					run(referenceExec("CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0) NO INHERIT)", 0))
					run(referenceExec("CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))", 0))
					run(referenceExec("INSERT INTO z VALUES(11)", 1))
					run(referenceExec("INSERT INTO a VALUES(22)", 1))
					// Independent address-order expectation: names are intentionally
					// reversed. Neither backend's observed Detail supplies this golden.
					s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"))
					s.prepared = prepared
					run(s)
					run(referenceSelect("SELECT a FROM z", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(referenceSelect("SELECT a FROM a", []string{"a"}, []string{"INT4"}, [][]any{{int64(22)}}))
					run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
				})
			}
		})
	}
}
