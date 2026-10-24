package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsForms(t *testing.T) {
	transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE txv_source(a INT,b INT)", 0),
			fixture.Exec("INSERT INTO txv_source VALUES(1,10)", 1),
			fixture.Exec("CREATE VIEW txv_checked AS SELECT b,a FROM txv_source WHERE a<b WITH LOCAL CHECK OPTION", 0),
			fixture.Exec("CREATE VIEW txv_nested AS SELECT a,b FROM txv_checked WHERE a>0 WITH CASCADED CHECK OPTION", 0),
			fixture.Exec("CREATE RECURSIVE VIEW txv_recursive(n) AS SELECT 1 UNION ALL SELECT n+1 FROM txv_recursive WHERE n<3", 0),
			fixture.Exec("CREATE TEMP VIEW txv_temporary AS SELECT 9 AS n", 0),
		} {
			transactionalViewsExec(t, p, b, e, held, s)
		}
		run(fixture.Select("SELECT n FROM txv_recursive ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		run(fixture.Select("SELECT n FROM txv_temporary", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("CREATE TEMP VIEW txv_temporary AS SELECT 8 AS n", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT n FROM txv_temporary", []string{"n"}, []string{"INT4"}, [][]any{{int64(8)}}))
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("INSERT INTO txv_nested(a,b) VALUES(2,20)", 1))
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("UPDATE txv_checked SET b=21 WHERE a=2", 1))
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("DELETE FROM txv_nested WHERE a=1", 1))
		run(fixture.Select("SELECT a,b FROM txv_source", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}}))
		if mode != "autocommit" {
			run(fixture.Exec("SAVEPOINT checked", 0))
		}
		transactionalViewsExec(t, p, b, e, held, viewCheckError("INSERT INTO txv_checked(b,a) VALUES(4,40)", "txv_checked", "40, 4"))
		if mode != "autocommit" {
			run(fixture.Exec("ROLLBACK TO checked", 0))
		}
		run(fixture.Select("SELECT a,b FROM txv_source", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}}))
		finish(false)
		if mode != "autocommit" {
			for _, name := range []string{"txv_source", "txv_checked", "txv_nested", "txv_recursive"} {
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount(name, 0))
			}
		}
	})
}
