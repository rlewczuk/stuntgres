package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceCheckAlterCandidates(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(9,1,NULL)", 1))
					run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT c,ALTER b SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"}))
					run(referenceImplicitError("INSERT INTO t VALUES(9,2,NULL)", referenceCheckViolation("t", "c", "9, 2, null")))
					run(referenceSelect("SELECT * FROM t", []string{"p", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), nil}}))
					run(referenceExec("ALTER TABLE t DROP p,DROP b", 0))
					run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
					run(referenceExec("ALTER TABLE t DROP CONSTRAINT c", 0))
					run(referenceExec("INSERT INTO t VALUES(2)", 1))
				})
			}
		})
	}
}

func TestReferenceCheckAlterCandidateTransactions(t *testing.T) {
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
					defer conn.ExecContext(p.ctx, "ROLLBACK")
					run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(9,1,NULL)", 1))
					run(referenceExec("ALTER TABLE t DROP COLUMN p,DROP COLUMN b", 0))
					for _, q := range []string{"BEGIN", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK"} {
						n := int64(0)
						if q == "INSERT INTO t VALUES(2)" {
							n = 1
						}
						run(referenceExec(q, n))
					}
					run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
					for _, q := range []string{"BEGIN", "SAVEPOINT keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK TO keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "COMMIT"} {
						n := int64(0)
						if q == "INSERT INTO t VALUES(2)" {
							n = 1
						}
						run(referenceExec(q, n))
					}
					run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceCheckAlterTransactionalPublication(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			peer, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer a.ExecContext(p.ctx, "ROLLBACK")
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, a, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			for _, q := range []string{"BEGIN", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(referenceExec(q, n))
			}
			referenceCheckRun(t, p.ctx, peer, b.recorder, referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
			referenceCheckRun(t, p.ctx, peer, b.recorder, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
			for _, q := range []string{"BEGIN", "SAVEPOINT keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK TO keep_check", "SAVEPOINT attempt"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(referenceExec(q, n))
			}
			run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "2"), 2)))
			for _, q := range []string{"ROLLBACK TO attempt", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "COMMIT"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(referenceExec(q, n))
			}
			referenceCheckRun(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO t VALUES(2)", 1))
			s := referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}})
			s.bag = true
			referenceCheckRun(t, p.ctx, peer, b.recorder, s)
		})
	}
}

func TestReferenceCheckAlterOwnedSequenceCandidate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE SEQUENCE s", 0))
					run(referenceExec("CREATE TABLE u(p INT,a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
					run(referenceExec("ALTER SEQUENCE s OWNED BY u.p", 0))
					run(referenceExec("ALTER TABLE u DROP CONSTRAINT c,DROP p", 0))
					run(referenceExec("INSERT INTO u VALUES(2)", 1))
					run(referenceSelect("SELECT * FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceCheckAlterOwnedSequenceAutoConstraint(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE SEQUENCE s", 0))
			run(referenceExec("CREATE TABLE t(p INT,a INT,CONSTRAINT c CHECK(p>0 AND nextval('s')>0))", 0))
			run(referenceExec("ALTER SEQUENCE s OWNED BY t.p", 0))
			run(referenceExec("ALTER TABLE t DROP p", 0))
			run(referenceExec("INSERT INTO t VALUES(2)", 1))
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	}
}
