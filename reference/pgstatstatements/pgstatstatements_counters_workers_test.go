package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersWorkers(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		s.exec("CREATE TABLE pgss_part.pgss_counter_workers(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_workers VALUES(1),(2),(3),(4),(5),(6)", 6))
		for _, q := range []string{"SET max_parallel_workers_per_gather=2", "SET parallel_setup_cost=0", "SET parallel_tuple_cost=0", "SET min_parallel_table_scan_size=0"} {
			s.work(fixture.Exec(q, 0))
		}
		q := "SELECT k FROM pgss_part.pgss_counter_workers parallel_projection"
		step := fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}})
		step.Bag = true
		s.counts(q, 0, 0, 0)
		plan, err := s.worker.QueryContext(s.p.Ctx, "EXPLAIN "+q)
		if err != nil {
			s.t.Fatal(err)
		}
		if !plan.Next() {
			s.t.Fatalf("EXPLAIN returned no plan: %v", plan.Err())
		}
		for plan.Next() {
		}
		if err := plan.Err(); err != nil {
			s.t.Fatal(err)
		}
		plan.Close()
		s.counts(q, 0, 0, 0) // EXPLAIN without execution launches nothing.
		s.work(step)
		pgssCounterCheck(s, q, 1, 6, pgssCounterWant{read: 1, hit: 1, planned: 2, launched: 2, pgShared: true})
		s.work(fixture.Exec("SET track_counts=off", 0))
		s.work(fixture.Prepared(step))
		pgssCounterCheck(s, q, 2, 12, pgssCounterWant{read: 1, hit: 3, planned: 4, launched: 4, pgShared: true})
		// Native SUM has no partial-state provider. Preserve its exact value
		// and serial boundary rather than infer launches from settings.
		q = "SELECT sum(k) AS total FROM pgss_part.pgss_counter_workers parallel_aggregate"
		s.counts(q, 0, 0, 0)
		s.work(fixture.Select(q, []string{"total"}, []string{"INT8"}, [][]any{{int64(21)}}))
		pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true})
		// Existing string_agg supports real partial states. Equal inputs
		// keep the output exact without imposing a parallel arrival order.
		q = "SELECT string_agg($1::text,$2::text) AS total FROM pgss_part.pgss_counter_workers parallel_string_aggregate"
		s.counts(q, 0, 0, 0)
		s.work(fixture.Select(q, []string{"total"}, []string{"TEXT"}, [][]any{{"xxxxxx"}}, "x", ""))
		pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 2, planned: 2, launched: 2, pgShared: true})
		q = "SELECT k/(k-k) AS v FROM pgss_part.pgss_counter_workers failed_workers"
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		failure.SelectRows = true
		s.work(failure)
		s.counts(q, 0, 0, 0) // Real launches do not publish a failed execution.
		s.work(fixture.Exec("SET max_parallel_workers_per_gather=0", 0))
		q = "SELECT k FROM pgss_part.pgss_counter_workers serial_projection"
		step.Query = q
		s.work(step)
		pgssCounterCheck(s, q, 1, 6, pgssCounterWant{hit: 1, pgShared: true, serial: true})
		// Timing/JIT switches do not fabricate physical I/O or machine code.
		for _, q := range []string{"SET track_io_timing=on", "SET track_wal_io_timing=on", "SET jit=on"} {
			s.work(fixture.Exec(q, 0))
		}
		q = "SELECT k FROM pgss_part.pgss_counter_workers absent_mechanisms"
		step.Query = q
		s.work(step)
		// PostgreSQL's default JIT threshold does not compile this tiny scan.
		pgssCounterCheck(s, q, 1, 6, pgssCounterWant{hit: 1, pgShared: true, serial: true})
	})
}
