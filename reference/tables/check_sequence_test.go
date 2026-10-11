package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckVolatileSequenceDependency(t *testing.T) {
	for _, cascadePrepared := range []bool{false, true} {
		t.Run(fmt.Sprint(cascadePrepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
					run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
					run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
					if !b.PG {
						fixture.CheckDump(t, p)
					}
					run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
					run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
					if !b.PG {
						fixture.CheckRoundtrip(t, p, fixture.CheckDump(t, p))
					}
					run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
					run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(3)}}))
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError("DROP SEQUENCE s", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Exec("INSERT INTO t VALUES(4)", 1))
					run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(4)}}))
					s := fixture.Exec("DROP SEQUENCE s CASCADE", 0)
					s.Prepared = cascadePrepared
					run(s)
					run(fixture.Exec("INSERT INTO t VALUES(5)", 1))
					run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
				})
			}
		})
	}
}

func TestReferenceCheckSequenceDropTransactionalCandidates(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT before_drop", 0))
			run(fixture.Exec("DROP SEQUENCE s CASCADE", 0))
			run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
			run(fixture.Exec("ROLLBACK TO before_drop", 0))
			run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Prepared(fixture.ImplicitError("DROP SEQUENCE s", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP SEQUENCE s CASCADE", 0))
			run(fixture.Exec("INSERT INTO t VALUES(4)", 1))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("INSERT INTO t VALUES(5)", 1))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(5)}}))
		})
	}
}
