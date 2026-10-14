package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryParallelSettings(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { queryParallelRun(t, p, b, c, s) }
		show := func(value string) {
			run(fixture.Select("SHOW max_parallel_workers_per_gather", []string{"max_parallel_workers_per_gather"}, []string{"TEXT"}, [][]any{{value}}))
			run(fixture.Prepared(fixture.Select("SELECT current_setting('max_parallel_workers_per_gather')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{value}})))
		}
		show("2")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, workers := range []string{"0", "1", "2", "1024"} {
			queryParallelWorkers(t, p, b, c, workers)
			show(workers)
			queryParallelRun(t, p, b, peer, fixture.Select("SHOW max_parallel_workers_per_gather", []string{"max_parallel_workers_per_gather"}, []string{"TEXT"}, [][]any{{"2"}}))
		}
		for _, tc := range []struct{ input, message string }{
			{"-1", `-1 is outside the valid range for parameter "max_parallel_workers_per_gather" (0 .. 1024)`},
			{"1025", `1025 is outside the valid range for parameter "max_parallel_workers_per_gather" (0 .. 1024)`},
			{"'bogus'", `invalid value for parameter "max_parallel_workers_per_gather": "bogus"`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SET max_parallel_workers_per_gather="+tc.input, queryParallelDiagnostic("22023", tc.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				show("1024")
			}
		}
		run(fixture.Exec("RESET max_parallel_workers_per_gather", 0))
		show("2")
		run(fixture.Exec("SET max_parallel_workers_per_gather=7", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL max_parallel_workers_per_gather=0", 0))
		show("0")
		run(fixture.Exec("SAVEPOINT worker_setting", 0))
		run(fixture.Exec("SET LOCAL max_parallel_workers_per_gather=1", 0))
		show("1")
		run(fixture.Exec("ROLLBACK TO worker_setting", 0))
		show("0")
		run(fixture.Exec("COMMIT", 0))
		show("7")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		show("2")
		run(fixture.Exec("ROLLBACK", 0))
		show("7")
		run(fixture.Exec("RESET ALL", 0))
		show("2")
	})
}

func TestReferenceQueryParallelErrors(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { queryParallelRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE parallel_div(id INT NOT NULL,d INT)", 0))
		run(fixture.Exec("INSERT INTO parallel_div VALUES(1,2),(2,1),(3,0),(4,2),(5,1),(6,2)", 6))
		for _, q := range []string{
			"SELECT id,10/d AS v FROM parallel_div",
			"SELECT id FROM parallel_div WHERE 10/d>0",
			"SELECT id FROM parallel_div ORDER BY 10/d,id",
		} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, workers := range []string{"0", "2", "7"} {
				queryParallelWorkers(t, p, b, c, workers)
				for _, x := range []fixture.Executor{c, held} {
					queryParallelFailure(t, p.Ctx, b, x, q, queryParallelDiagnostic("22012", "division by zero"))
					run(fixture.Select("SELECT id FROM parallel_div WHERE d=0", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
				}
			}
			stmt.Close()
		}
		// A rejected occurrence cannot execute the failing target.
		run(fixture.Select("SELECT id,10/d AS v FROM parallel_div WHERE d<>0 ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}, {int64(2), int64(10)}, {int64(4), int64(5)}, {int64(5), int64(10)}, {int64(6), int64(5)}}))
		run(fixture.Exec("BEGIN", 0))
		q := "SELECT 10/d FROM parallel_div"
		// One execution in the transaction: the following command must see abort.
		rows, err := c.QueryContext(p.Ctx, q)
		if rows != nil {
			defer rows.Close()
			if !b.PG {
				t.Fatal("native error published rows")
			}
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		want := queryParallelDiagnostic("22012", "division by zero")
		fixture.AssertDiagnostic(t, err, &want)
		failure := fixture.ErrorDiagnostic("SELECT id FROM parallel_div", queryParallelDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		failure.SelectRows = true
		run(failure)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT id FROM parallel_div WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
