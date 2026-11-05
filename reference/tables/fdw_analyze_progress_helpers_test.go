package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"stuntgres/reference/fixture"
)

// Keep provider installation, committed independent SQL and teardown on the
// existing FDW fixtures.
func fdwAnalyzeProgressScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwSQLReader, func(fixture.Step))) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"fdw_owner", "fdw_other", "fdw_reader"})
	reader := newFDWSQLReader(t)
	p := fixture.NewScenarioWithDriverConfig(t, reader.configure)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "fdw_source", "fdw_import", "fdw_empty")
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			t.Cleanup(func() { fdwCleanup(t, c) })
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			body(t, p, b, c, reader, run)
		})
	}
}

type fdwAnalyzeProgressIdentity struct {
	pid, database, relation int64
	name                    string
}

func fdwAnalyzeProgressIdentify(t *testing.T, p *fixture.Pair, c *sql.Conn, relation string) fdwAnalyzeProgressIdentity {
	t.Helper()
	var id fdwAnalyzeProgressIdentity
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),d.oid,current_database(),$1::regclass::oid FROM pg_database d WHERE datname=current_database()", relation).Scan(&id.pid, &id.database, &id.name, &id.relation); err != nil {
		t.Fatal(err)
	}
	if id.pid <= 0 || id.database <= 0 || id.relation <= 0 || id.name == "" {
		t.Fatal("invalid physical worker identity", id)
	}
	return id
}

const fdwAnalyzeProgressColumns = "pid,datid,datname,relid,phase,sample_blks_total,sample_blks_scanned,ext_stats_total,ext_stats_computed,child_tables_total,child_tables_done,current_child_table_relid,delay_time"

// Samples are deliberately supplied by the independently expected actual held
// boundary. They are not learned from the native producer or compared by parity.
func fdwAnalyzeProgressObserve(t *testing.T, p *fixture.Pair, b fixture.Backend, id fdwAnalyzeProgressIdentity, phase string, sampleTotal, sampleDone, extTotal, extDone, children, done, child int64, finished <-chan struct{}) {
	t.Helper()
	query := fmt.Sprintf("SELECT %s FROM pg_catalog.pg_stat_progress_analyze WHERE pid=%d", fdwAnalyzeProgressColumns, id.pid)
	for _, prepared := range []bool{false, true} {
		select {
		case <-finished:
			t.Fatal("ANALYZE finished before live observation")
		default:
		}
		step := fixture.Select(query,
			[]string{"pid", "datid", "datname", "relid", "phase", "sample_blks_total", "sample_blks_scanned", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done", "current_child_table_relid", "delay_time"},
			[]string{"INT4", "OID", "NAME", "OID", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "OID", "FLOAT8"},
			[][]any{{id.pid, id.database, id.name, id.relation, phase, sampleTotal, sampleDone, extTotal, extDone, children, done, child, float64(0)}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
}

func fdwAnalyzeProgressAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, id fdwAnalyzeProgressIdentity) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step := fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_catalog.pg_stat_progress_analyze WHERE pid=%d", id.pid), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
}

func fdwAnalyzeProgressExecute(ctx context.Context, c *sql.Conn, statement *sql.Stmt, query string) fixture.RelationOutcome {
	var out fixture.RelationOutcome
	if statement != nil {
		out.Result, out.Err = statement.ExecContext(ctx)
	} else {
		out.Result, out.Err = c.ExecContext(ctx, query)
	}
	return out
}

func fdwAnalyzeProgressPrepare(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, prepared bool) *sql.Stmt {
	t.Helper()
	if !prepared {
		return nil
	}
	statement, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { statement.Close() })
	return statement
}

type fdwAnalyzeProgressGate struct {
	cancel   context.CancelFunc
	release  func()
	join     func() fixture.RelationOutcome
	finished <-chan struct{}
}

// These are actual registered-provider callbacks. PostgreSQL has no equivalent
// native control; its stock postgres_fdw path is exercised by the shared SQL.
func fdwAnalyzeProgressHold(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, query, phase string, prepared bool, failure error) fdwAnalyzeProgressGate {
	t.Helper()
	statement := fdwAnalyzeProgressPrepare(t, p, c, query, prepared)
	ctx, cancel := context.WithCancel(p.Ctx)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Register ownership before installing the callback or starting its worker.
	t.Cleanup(func() { cancel(); unblock(); r.setHook(nil) })
	r.setHook(func(ctx context.Context, name string) error {
		if name != phase {
			return ctx.Err()
		}
		enterOnce.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			if failure != nil {
				return failure
			}
			return ctx.Err()
		}
	})
	wait := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		return fdwAnalyzeProgressExecute(ctx, c, statement, query)
	})
	select {
	case <-entered:
	case <-finished:
		out := wait()
		t.Fatal("provider gate not reached", phase, out.Err)
	case <-p.Ctx.Done():
		t.Fatal("provider admission", phase, p.Ctx.Err())
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT 42 AS peer", []string{"peer"}, []string{"INT4"}, [][]any{{int64(42)}}))
	return fdwAnalyzeProgressGate{cancel: cancel, release: unblock, finished: finished, join: func() fixture.RelationOutcome {
		out := wait()
		r.setHook(nil)
		return out
	}}
}

func fdwAnalyzeProgressSuccess(t *testing.T, out fixture.RelationOutcome) {
	t.Helper()
	if out.Err != nil || out.Rows != nil || out.Result == nil {
		t.Fatal("ANALYZE did not complete", out.Err, out.Rows, out.Result)
	}
	if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
		t.Fatal("ANALYZE affected rows", n, err)
	}
}

func fdwAnalyzeProgressCanceled(t *testing.T, b fixture.Backend, out fixture.RelationOutcome) {
	t.Helper()
	if out.Result != nil || out.Rows != nil {
		t.Fatal("canceled ANALYZE published result", out)
	}
	if b.PG {
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else if !errors.Is(out.Err, context.Canceled) {
		t.Fatal("ANALYZE cancellation", out.Err)
	}
}

func fdwAnalyzeProgressDirectProducts(run func(fixture.Step)) {
	fdwAnalyzeDirectJoint(run, 3)
	run(fixture.Select("SELECT stxddependencies::text AS dependencies"+fdwAnalyzeDirectData, []string{"dependencies"}, []string{"TEXT"}, [][]any{{`{"1 => 2": 1.000000, "2 => 1": 1.000000}`}}))
	run(fixture.Select("SELECT m.values::text AS values,m.nulls::text AS nulls,m.frequency,abs(m.base_frequency-m.frequency*m.frequency)<0.000000000001 AS independent_mass FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='fdw_direct_joint' AND NOT d.stxdinherit ORDER BY m.values::text", []string{"values", "nulls", "frequency", "independent_mass"}, []string{"TEXT", "TEXT", "FLOAT8", "BOOL"}, [][]any{{"{0,0}", "{f,f}", float64(0.334), true}, {"{1,1}", "{f,f}", float64(0.333), true}, {"{2,2}", "{f,f}", float64(0.333), true}}))
	fdwAnalyzeDirectExpression(run, 3, "")
}

func fdwAnalyzeProgressResources(t *testing.T, r *fdwSQLReader) {
	t.Helper()
	if state := r.snapshot(); state.openPools != 0 || state.openRows != 0 || state.openCursors != 0 {
		t.Fatal("ANALYZE retained provider resources", state)
	}
}
