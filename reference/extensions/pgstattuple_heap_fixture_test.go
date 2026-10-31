package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var heapStatsNames = []string{"table_len", "tuple_count", "tuple_len", "tuple_percent", "dead_tuple_count", "dead_tuple_len", "dead_tuple_percent", "free_space", "free_percent"}
var heapStatsTypes = []string{"INT8", "INT8", "INT8", "FLOAT8", "INT8", "INT8", "FLOAT8", "INT8", "FLOAT8"}
var heapApproxNames = []string{"table_len", "scanned_percent", "approx_tuple_count", "approx_tuple_len", "approx_tuple_percent", "dead_tuple_count", "dead_tuple_len", "dead_tuple_percent", "approx_free_space", "approx_free_percent"}
var heapApproxTypes = []string{"INT8", "FLOAT8", "INT8", "INT8", "FLOAT8", "INT8", "INT8", "FLOAT8", "INT8", "FLOAT8"}

func heapStatsCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "hs_part")
	fixture.SchemaReferenceCleanup(t, "hs_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := heapStatsConn(t, p, b)
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgstattuple CASCADE", "DROP SCHEMA IF EXISTS hs_part CASCADE", "DROP SCHEMA IF EXISTS hs_moved CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(q, err) }
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA hs_part", "CREATE EXTENSION pgstattuple SCHEMA hs_part", "CREATE TABLE hs_part.heap(k integer)", "ALTER TABLE hs_part.heap SET(autovacuum_enabled=false)", "CREATE INDEX heap_index ON hs_part.heap(k)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func heapStatsConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	return c
}

func heapStatsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func heapStatsEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, relation string) {
	t.Helper()
	for _, typ := range []string{"text", "regclass"} {
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pgstattuple($1::"+typ+")", heapStatsNames, heapStatsTypes,
			[][]any{{int64(0), int64(0), int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), float64(0)}}, relation))
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pg_relpages($1::"+typ+")", []string{"pg_relpages"}, []string{"INT8"}, [][]any{{int64(0)}}, relation))
	}
	heapStatsBoth(t, p, b, c, fixture.Select("SELECT * FROM hs_part.pgstattuple_approx($1::regclass)", heapApproxNames, heapApproxTypes,
		[][]any{{int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), float64(0)}}, relation))
}

// Counts are exact and independently supplied. Populated physical values are
// constrained by release equations, never learned from either backend.
// Group round's result before CAST: the shared scalar grammar admits a grouped
// function child there, not an ungrouped selected call. Keep the same equations.
func heapStatsCounts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, relation string, exactLive, exactDead, approxLive, approxDead int64) {
	t.Helper()
	for _, typ := range []string{"text", "regclass"} {
		q := "SELECT tuple_count,dead_tuple_count,table_len>0 AND table_len=hs_part.pg_relpages($1::"+typ+")*8192 AND ((tuple_count=0 AND tuple_len=0) OR (tuple_count>0 AND tuple_len>0)) AND ((dead_tuple_count=0 AND dead_tuple_len=0) OR (dead_tuple_count>0 AND dead_tuple_len>0)) AND free_space>=0 AND tuple_len+dead_tuple_len+free_space<table_len AND tuple_percent=CAST((round(CAST(100.0*tuple_len/table_len AS numeric),2)) AS float8) AND dead_tuple_percent=CAST((round(CAST(100.0*dead_tuple_len/table_len AS numeric),2)) AS float8) AND free_percent=CAST((round(CAST(100.0*free_space/table_len AS numeric),2)) AS float8) AS valid FROM hs_part.pgstattuple($1::"+typ+")"
		heapStatsBoth(t, p, b, c, fixture.Select(q, []string{"tuple_count", "dead_tuple_count", "valid"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{exactLive, exactDead, true}}, relation))
	}
	q := "SELECT approx_tuple_count,dead_tuple_count,table_len>0 AND table_len=hs_part.pg_relpages($1::regclass)*8192 AND scanned_percent>=0 AND scanned_percent<=100 AND ((approx_tuple_count=0 AND approx_tuple_len=0) OR (approx_tuple_count>0 AND approx_tuple_len>0)) AND ((dead_tuple_count=0 AND dead_tuple_len=0) OR (dead_tuple_count>0 AND dead_tuple_len>0)) AND approx_free_space>=0 AND approx_tuple_len+dead_tuple_len+approx_free_space<=table_len AND approx_tuple_percent>=0 AND approx_tuple_percent<=100 AND dead_tuple_percent>=0 AND dead_tuple_percent<=100 AND approx_free_percent>=0 AND approx_free_percent<=100 AS valid FROM hs_part.pgstattuple_approx($1::regclass)"
	heapStatsBoth(t, p, b, c, fixture.Select(q, []string{"approx_tuple_count", "dead_tuple_count", "valid"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{approxLive, approxDead, true}}, relation))
}

// Rollback can leave genuine aborted physical tuples in PostgreSQL. Acceptance
// requires their exclusion from live counts, not fabricated byte/dead parity.
func heapStatsLive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, live int64) {
	t.Helper()
	for _, spec := range []struct{ call, column string }{{"pgstattuple", "tuple_count"}, {"pgstattuple_approx", "approx_tuple_count"}} {
		heapStatsBoth(t, p, b, c, fixture.Select("SELECT "+spec.column+" AS live FROM hs_part."+spec.call+"('hs_part.heap'::regclass)", []string{"live"}, []string{"INT8"}, [][]any{{live}}))
	}
}

// Establish the snapshot before a writer changes anything, retaining genuine
// predecessors and preventing opportunistic pruning during the count schedule.
func heapStatsHorizon(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := heapStatsConn(t, p, b)
	ready := false
	defer func() {
		if !ready {
			c.ExecContext(p.Ctx, "ROLLBACK")
			c.Close()
		}
	}()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM hs_part.heap", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	ready = true
	return c
}
