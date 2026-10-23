package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwParallelSecurity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE ROLE fdw_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
	q := "SELECT id FROM fdw_data ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	denied := fdwDiagnostic("42501", "permission denied for foreign table fdw_data")
	checkDenied := func(d fixture.Diagnostic) {
		before, parallel := r.snapshot(), r.parallelCounts()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d))
		if !b.PG && (r.snapshot() != before || r.parallelCounts() != parallel) {
			t.Fatal("authorization denial entered provider")
		}
	}
	run(fixture.Exec("SET ROLE fdw_reader", 0))
	checkDenied(denied)
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT SELECT ON fdw_data TO fdw_reader", 0))
	run(fixture.Exec("SET ROLE fdw_reader", 0))
	checkDenied(fdwDiagnostic("42704", `user mapping not found for user "fdw_reader", server "fdw_live"`))
	run(fixture.Exec("RESET ROLE", 0))
	options := "user 'postgres'"
	if b.PG {
		options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
	}
	run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
	run(fixture.Exec("SET ROLE fdw_reader", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fdwParallelIDs(q, 1, 2, 3, 4, 5, 6))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("REVOKE SELECT ON fdw_data FROM fdw_reader", 0))
	run(fixture.Exec("SET ROLE fdw_reader", 0))
	checkDenied(denied)
	run(fixture.Exec("RESET ROLE", 0))
}

func fdwParallelCorrelation(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwParallelReader, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE fdw_parallel_keys(ord integer,k integer)", 0))
	run(fixture.Exec("INSERT INTO fdw_parallel_keys VALUES(1,1),(2,2),(3,1)", 3))
	q := "SELECT keys.ord,(SELECT f.p FROM fdw_data f WHERE f.id=keys.k OFFSET 0) AS p FROM fdw_parallel_keys keys ORDER BY keys.ord"
	for _, prepared := range []bool{false, true} {
		before, parallel := r.snapshot(), r.parallelCounts()
		s := fixture.Select(q, []string{"ord", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "a"}})
		s.Prepared = prepared
		run(s)
		if !b.PG {
			after := r.snapshot()
			if after.begins-before.begins != 1 || after.rescans-before.rescans != 2 || r.parallelCounts() != parallel {
				t.Fatal("correlation did not use original serial ReScan", before, after)
			}
		}
	}
}
