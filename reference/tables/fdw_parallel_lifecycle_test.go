package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWParallelErrors(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT 10/(id-2) FROM fdw_data", fdwDiagnostic("22012", "division by zero"))
			s.Prepared = prepared
			run(s)
			run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4))
		}
		if !b.PG {
			fdwParallelFailures(t, p, c, r, run)
		}
	})
}

func TestReferenceFDWParallelCancellation(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		q := "SELECT id FROM fdw_data ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := stmt.QueryContext(ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("pre-canceled operation published rows")
		}
		if b.PG && !errors.Is(err, context.Canceled) {
			fixture.State(t, err, "57014")
			want := fdwDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fdwParallelIDs(q, 1, 2, 3, 4, 5, 6))
		if !b.PG {
			fdwParallelGate(t, p, c, r, true)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fdwParallelIDs(q, 1, 2, 3, 4, 5, 6))
		}
	})
}

func TestReferenceFDWParallelPersistence(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4))
		var dump bytes.Buffer
		if !b.PG {
			before := r.snapshot()
			parallel := r.parallelCounts()
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if r.snapshot() != before || r.parallelCounts() != parallel {
				t.Fatal("Dump invoked a provider")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		fdwSourceExec(t, p, b, r.fdwSQLReader, run, "UPDATE fdw_source.data SET k=2 WHERE id=6", 1)
		run(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4, 6))
		if !b.PG {
			fdwParallelRestoreTargets(t, p, r, dump.Bytes())
		}
	})
}

func TestReferenceFDWParallelFeatureSmoke(t *testing.T) {
	fdwParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		for _, setting := range []string{"0", "2"} {
			run(fixture.Exec("SET max_parallel_workers_per_gather="+setting, 0))
			run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY k DESC NULLS LAST,id", 1, 4, 3, 6, 2, 5))
			run(fixture.Prepared(fdwParallelIDs("SELECT id FROM fdw_data WHERE k=2 ORDER BY id", 1, 4)))
		}
		if !b.PG {
			fdwParallelGate(t, p, c, r, false)
			fdwParallelGate(t, p, c, r, true)
			run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
		}
	})
}
