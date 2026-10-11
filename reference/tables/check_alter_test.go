package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckAlterCandidates(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9,1,NULL)", 1))
					run(fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT c,ALTER b SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "b" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "b"}))
					run(fixture.ImplicitError("INSERT INTO t VALUES(9,2,NULL)", fixture.CheckViolation("t", "c", "9, 2, null")))
					run(fixture.Select("SELECT * FROM t", []string{"p", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(1), nil}}))
					run(fixture.Exec("ALTER TABLE t DROP p,DROP b", 0))
					run(fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
					run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT c", 0))
					run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
				})
			}
		})
	}
}

func TestReferenceCheckAlterCandidateTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "ROLLBACK")
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(p INT,a INT CONSTRAINT c CHECK(a=1),b INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9,1,NULL)", 1))
					run(fixture.Exec("ALTER TABLE t DROP COLUMN p,DROP COLUMN b", 0))
					for _, q := range []string{"BEGIN", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK"} {
						n := int64(0)
						if q == "INSERT INTO t VALUES(2)" {
							n = 1
						}
						run(fixture.Exec(q, n))
					}
					run(fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
					for _, q := range []string{"BEGIN", "SAVEPOINT keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK TO keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "COMMIT"} {
						n := int64(0)
						if q == "INSERT INTO t VALUES(2)" {
							n = 1
						}
						run(fixture.Exec(q, n))
					}
					run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceCheckAlterTransactionalPublication(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, a, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			for _, q := range []string{"BEGIN", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			fixture.CheckRun(t, p.Ctx, peer, b.Recorder, fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
			fixture.CheckRun(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
			for _, q := range []string{"BEGIN", "SAVEPOINT keep_check", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "ROLLBACK TO keep_check", "SAVEPOINT attempt"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			run(fixture.Prepared(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "c", "2"), 2)))
			for _, q := range []string{"ROLLBACK TO attempt", "ALTER TABLE t DROP CONSTRAINT c", "INSERT INTO t VALUES(2)", "COMMIT"} {
				n := int64(0)
				if q == "INSERT INTO t VALUES(2)" {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			fixture.CheckRun(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(2)", 1))
			s := fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}})
			s.Bag = true
			fixture.CheckRun(t, p.Ctx, peer, b.Recorder, s)
		})
	}
}

func TestReferenceCheckAlterOwnedSequenceCandidate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE s", 0))
					run(fixture.Exec("CREATE TABLE u(p INT,a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
					run(fixture.Exec("ALTER SEQUENCE s OWNED BY u.p", 0))
					run(fixture.Exec("ALTER TABLE u DROP CONSTRAINT c,DROP p", 0))
					run(fixture.Exec("INSERT INTO u VALUES(2)", 1))
					run(fixture.Select("SELECT * FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceCheckAlterOwnedSequenceAutoConstraint(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s", 0))
			run(fixture.Exec("CREATE TABLE t(p INT,a INT,CONSTRAINT c CHECK(p>0 AND nextval('s')>0))", 0))
			run(fixture.Exec("ALTER SEQUENCE s OWNED BY t.p", 0))
			run(fixture.Exec("ALTER TABLE t DROP p", 0))
			run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	}
}
