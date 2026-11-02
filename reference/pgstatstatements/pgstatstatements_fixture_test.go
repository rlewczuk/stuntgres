package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The released 1.12 declaration, not either backend's observed descriptors.
var pgssNames = []string{
	"userid", "dbid", "toplevel", "queryid", "query", "plans",
	"total_plan_time", "min_plan_time", "max_plan_time", "mean_plan_time", "stddev_plan_time",
	"calls", "total_exec_time", "min_exec_time", "max_exec_time", "mean_exec_time", "stddev_exec_time", "rows",
	"shared_blks_hit", "shared_blks_read", "shared_blks_dirtied", "shared_blks_written",
	"local_blks_hit", "local_blks_read", "local_blks_dirtied", "local_blks_written", "temp_blks_read", "temp_blks_written",
	"shared_blk_read_time", "shared_blk_write_time", "local_blk_read_time", "local_blk_write_time", "temp_blk_read_time", "temp_blk_write_time",
	"wal_records", "wal_fpi", "wal_bytes", "wal_buffers_full", "jit_functions", "jit_generation_time",
	"jit_inlining_count", "jit_inlining_time", "jit_optimization_count", "jit_optimization_time", "jit_emission_count", "jit_emission_time",
	"jit_deform_count", "jit_deform_time", "parallel_workers_to_launch", "parallel_workers_launched", "stats_since", "minmax_stats_since",
}

var pgssTypes = []string{
	"OID", "OID", "BOOL", "INT8", "TEXT", "INT8",
	"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8",
	"INT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT8",
	"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8",
	"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8",
	"INT8", "INT8", "NUMERIC", "INT8", "INT8", "FLOAT8",
	"INT8", "FLOAT8", "INT8", "FLOAT8", "INT8", "FLOAT8", "INT8", "FLOAT8", "INT8", "INT8", "TIMESTAMPTZ", "TIMESTAMPTZ",
}

type pgssScenario struct {
	t                *testing.T
	p                *fixture.Pair
	b                fixture.Backend
	observer, worker *sql.Conn
}

func pgssCase(t *testing.T, install bool, body func(*pgssScenario)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "pgss_part")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			observer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			s := &pgssScenario{t: t, p: p, b: b, observer: observer}
			// Inspection/setup never contributes to the measured shapes. History
			// belongs to the cluster; neither SQL reset nor empty history is assumed.
			s.exec("SET pg_stat_statements.track = 'none'")
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE", "DROP SCHEMA IF EXISTS pgss_part CASCADE"} {
						if _, err := observer.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			s.exec("CREATE SCHEMA pgss_part")
			if install {
				s.exec("CREATE EXTENSION pg_stat_statements SCHEMA pgss_part VERSION '1.12'")
				worker, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				s.worker = worker
				for _, q := range []string{"SET pg_stat_statements.track_utility = off", "SET pg_stat_statements.track_planning = off", "SET pg_stat_statements.track = 'top'"} {
					s.work(fixture.Exec(q, 0))
				}
			}
			body(s)
		})
	}
}

func (s *pgssScenario) run(step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.observer, s.b.Recorder, step)
}

func (s *pgssScenario) both(step fixture.Step) {
	s.t.Helper()
	s.run(step)
	s.run(fixture.Prepared(step))
}

func (s *pgssScenario) exec(query string) {
	s.t.Helper()
	s.run(fixture.Exec(query, 0))
}

func (s *pgssScenario) work(step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.worker, s.b.Recorder, step)
}

// Restrict assertions to the current principal/database and a new object/shape.
// Establish zero matching baseline before each measured stream, not zero cluster
// history. The aggregate is a bag assertion with no traversal-order assumption.
func (s *pgssScenario) counts(pattern string, groups, calls, rows int64) {
	s.t.Helper()
	q := "SELECT count(*) AS groups, COALESCE(sum(calls),0)::bigint AS calls, COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, []string{"groups", "calls", "rows"}, []string{"INT8", "INT8", "INT8"}, [][]any{{groups, calls, rows}}, pattern))
}

func (s *pgssScenario) entry(query string, calls, rows int64) {
	s.t.Helper()
	q := "SELECT queryid <> 0 AS identified,toplevel,calls,rows FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, []string{"identified", "toplevel", "calls", "rows"}, []string{"BOOL", "BOOL", "INT8", "INT8"}, [][]any{{true, true, calls, rows}}, query))
}
