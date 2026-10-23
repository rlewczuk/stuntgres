package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwParallelIDs(q string, ids ...int64) fixture.Step {
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return fixture.Select(q, []string{"id"}, []string{"INT4"}, rows)
}

func TestReferenceFDWParallelBags(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		if !b.PG {
			restore := fdwBorrowedBytes(r.fdwSQLReader)
			defer restore()
		}
		for _, ceiling := range []int{0, 1, 2, 7} {
			run(fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
			for _, prepared := range []bool{false, true} {
				bag := fixture.Select("SELECT k,p FROM fdw_data", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "a"}, {nil, "b"}, {int64(1), "c"}, {int64(2), "a"}, {nil, "b"}, {int64(1), nil}})
				bag.Prepared = prepared
				bag.Bag = true
				run(bag)
				s := fdwParallelIDs("SELECT id FROM fdw_data ORDER BY k DESC NULLS LAST,id", 1, 4, 3, 6, 2, 5)
				s.Prepared = prepared
				run(s)
				run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k>=2 ORDER BY id", 1, 4))
				run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=NULL"))
			}
		}
		for _, partitions := range []int{1, 2} {
			r.partitions = partitions
			run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
		}
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_zero() SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		previous := 6
		for _, n := range []int{0, 1, 2, 3, 7, 13} {
			fdwSourceExec(t, p, b, r.fdwSQLReader, run, "DELETE FROM fdw_source.data", int64(previous))
			for i := range n {
				fdwSourceExec(t, p, b, r.fdwSQLReader, run, fmt.Sprintf("INSERT INTO fdw_source.data VALUES(%d,NULL,NULL)", i+1), 1)
			}
			previous = n
			run(fixture.Select("SELECT count(*) FROM fdw_zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(n)}}))
			var rows [][]any
			for range n {
				rows = append(rows, []any{nil, nil})
			}
			s := fixture.Select("SELECT k,p FROM fdw_data", []string{"k", "p"}, []string{"INT4", "TEXT"}, rows)
			s.Bag = true
			run(s)
			var zero [][]any
			for range n {
				zero = append(zero, []any{})
			}
			run(fixture.Select("SELECT FROM fdw_zero", []string{}, []string{}, zero))
		}
	})
}

func TestReferenceFDWParallelParameters(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		q := "SELECT id FROM fdw_data WHERE k=$1 ORDER BY id"
		before, parallel := r.snapshot(), r.parallelCounts()
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if !b.PG && (r.snapshot() != before || r.parallelCounts() != parallel) {
			t.Fatal("quiet Prepare called provider")
		}
		// These arguments drive local quals over real source rows. A NULL
		// equality can reject every row before demanding the foreign source,
		// so the last provider request need not belong to that execution.
		// Independent held results, including recovery after NULL, prove binding.
		for _, ceiling := range []int{0, 2} {
			run(fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
			for _, tc := range []struct {
				arg any
				ids []int64
			}{{int64(2), []int64{1, 4}}, {int64(1), []int64{3, 6}}, {nil, nil}, {int64(2), []int64{1, 4}}} {
				s := fdwParallelIDs(q, tc.ids...)
				s.Args = []any{tc.arg}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			}
		}
		fdwSourceExec(t, p, b, r.fdwSQLReader, run, "UPDATE fdw_source.data SET k=2 WHERE id=6", 1)
		s := fdwParallelIDs(q, 1, 4, 6)
		s.Args = []any{int64(2)}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		fdwParallelCorrelation(t, p, b, r, run)
		fdwParallelSecurity(t, p, b, c, r, run)
	})
}

func TestReferenceFDWParallelEligibility(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		before := r.parallelCounts()
		r.safe = false
		run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
		r.safe = true
		run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id LIMIT 1", 1))
		run(fdwParallelIDs("SELECT id FROM fdw_data LIMIT 0"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_parallel_cursor CURSOR FOR SELECT id FROM fdw_data ORDER BY id", 0))
		run(fdwParallelIDs("FETCH ALL FROM fdw_parallel_cursor", 1, 2, 3, 4, 5, 6))
		run(fixture.Exec("CLOSE fdw_parallel_cursor", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("CREATE TABLE fdw_parallel_copy AS SELECT id FROM fdw_data", 6))
		run(fdwParallelIDs("SELECT id FROM fdw_parallel_copy ORDER BY id", 1, 2, 3, 4, 5, 6))
		if !b.PG && r.parallelCounts() != before {
			t.Fatal("ineligible SQL launched partition coordinator", before, r.parallelCounts())
		}
		if !b.PG {
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_scan_only_handler", 0))
			run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
			if r.parallelCounts() != before {
				t.Fatal("missing capability started coordination")
			}
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
		}
		fdwParallelHierarchy(t, b, r, run)
	})
}
