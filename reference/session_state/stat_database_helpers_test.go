package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var statDatabaseNames = []string{"datid", "datname", "numbackends", "xact_commit", "xact_rollback", "blks_read", "blks_hit", "tup_returned", "tup_fetched", "tup_inserted", "tup_updated", "tup_deleted", "conflicts", "temp_files", "temp_bytes", "deadlocks", "checksum_failures", "checksum_last_failure", "blk_read_time", "blk_write_time", "session_time", "active_time", "idle_in_transaction_time", "sessions", "sessions_abandoned", "sessions_fatal", "sessions_killed", "parallel_workers_to_launch", "parallel_workers_launched", "stats_reset"}
var statDatabaseTypes = []string{"OID", "NAME", "INT4", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ"}

// All exact deltas are observed from a different database, so observer SELECTs
// cannot contaminate the target's top-transaction outcomes.
type statDatabaseVector struct {
	Commit, Rollback, Insert, Update, Delete int64
	Sessions, Abandoned, Fatal, Killed       int64
	Planned, Launched, Deadlocks, Backends   int64
}

func statDatabaseRead(ctx context.Context, c *sql.Conn, name string) (v statDatabaseVector, err error) {
	err = c.QueryRowContext(ctx, "SELECT xact_commit,xact_rollback,tup_inserted,tup_updated,tup_deleted,sessions,sessions_abandoned,sessions_fatal,sessions_killed,parallel_workers_to_launch,parallel_workers_launched,deadlocks,numbackends FROM pg_catalog.pg_stat_database WHERE datname=$1", name).Scan(&v.Commit, &v.Rollback, &v.Insert, &v.Update, &v.Delete, &v.Sessions, &v.Abandoned, &v.Fatal, &v.Killed, &v.Planned, &v.Launched, &v.Deadlocks, &v.Backends)
	return
}
func statDatabaseGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) statDatabaseVector {
	t.Helper()
	v, err := statDatabaseRead(p.Ctx, c, name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func statDatabaseAwait(t *testing.T, p *fixture.Pair, c *sql.Conn, name string, want func(statDatabaseVector) bool) statDatabaseVector {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var last statDatabaseVector
	samples := 0
	for {
		v, err := statDatabaseRead(ctx, c, name)
		if err != nil {
			t.Fatalf("database %s publication read failed after %d samples, last=%+v: %v (wait context: %v; scenario context: %v)", name, samples, last, err, ctx.Err(), p.Ctx.Err())
		}
		last = v
		samples++
		if want(v) {
			return v
		}
		select {
		case <-ctx.Done():
			t.Fatalf("database %s publication expired after %d samples, last=%+v: %v (scenario context: %v)", name, samples, last, ctx.Err(), p.Ctx.Err())
		case <-ticker.C:
		}
	}
}
func statDatabaseExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, q string, n int64) {
	t.Helper()
	step := fixture.Exec(q, n)
	step.Prepared = prepared
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
}
func statDatabaseConn(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, func()) {
	t.Helper()
	return statsReportingConn(t, p, b)
}
func statDatabaseScenario(t *testing.T, suffix string, fn func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(string) fixture.Backend)) {
	t.Helper()
	var names []string
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			observer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
			create := func(label string) fixture.Backend {
				name := "stat_db_" + suffix + "_" + label
				names = append(names, name)
				statsExec(t, p, b, observer, "CREATE DATABASE "+name, 0)
				pool, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, name, "")
				return fixture.Backend{Name: name, DB: pool, Recorder: rec, PG: b.PG}
			}
			fn(t, p, b, observer, create)
		})
	}
}
func statDatabaseSetup(t *testing.T, p *fixture.Pair, target fixture.Backend, observer *sql.Conn) statDatabaseVector {
	t.Helper()
	c, retire := statDatabaseConn(t, p, target)
	for _, q := range []string{"CREATE TABLE stat_db_rows(id integer)", "CREATE TABLE stat_db_marker(id integer)", "INSERT INTO stat_db_marker VALUES(0)"} {
		n := int64(0)
		if q == "INSERT INTO stat_db_marker VALUES(0)" {
			n = 1
		}
		statDatabaseExec(t, p, target, c, false, q, n)
	}
	if target.PG {
		statDatabaseSettleReferenceCatalog(t, p, target, c)
	}
	retire()
	return statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Insert > 0 && v.Sessions == 1 && v.Backends == 0 })
}

// REL_18_6 InitPostgres starts and commits its own catalog-access transaction.
// Native admission reads published catalog state without beginning a Tx owner;
// successful registration must not invent a native SQL transaction for parity.
func statDatabaseStartupCommits(b fixture.Backend) int64 {
	if b.PG {
		return 1
	}
	return 0
}

// Establish an actual published baseline on the measured live writer. The
// marker INSERT is one known implicit commit and one insert, not measured DML.
// Wait for the unchanged ordinary/idle-retry publication, never force a flush.
// Startup, named preparation and the marker independently specify a minimum
// committed total, not equality with PostgreSQL's catalog/startup bookkeeping.
// Retain the actual published commit baseline; every other field is exact, and
// all subsequent measured outcomes remain exact deltas from that observed value.
func statDatabaseWriterBaseline(t *testing.T, p *fixture.Pair, target fixture.Backend, observer, writer *sql.Conn, before statDatabaseVector, preparationCommits int64) statDatabaseVector {
	t.Helper()
	statsExec(t, p, target, writer, "INSERT INTO stat_db_marker VALUES(99)", 1)
	want := before
	want.Commit += statDatabaseStartupCommits(target) + preparationCommits + 1
	want.Insert++
	want.Sessions++
	want.Backends++
	defer func() {
		if t.Failed() {
			t.Logf("database %s writer baseline before=%+v minimum=%+v (Commit >= minimum; every other field exact)", target.Name, before, want)
		}
	}()
	return statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool {
		if v.Commit < want.Commit {
			return false
		}
		v.Commit = want.Commit
		return v == want
	})
}

// Full-column ANALYZE resets the shared pg_database analyze eligibility and
// performs its real pg_statistic writes before the setup writer is retired.
// A predecessor autovacuum worker must also exit: its final statistics drain
// precedes removal from pg_stat_activity, so none can cross the setup cut.
// Native pg_database is virtual; it has no corresponding background maintenance.
func statDatabaseSettleReferenceCatalog(t *testing.T, p *fixture.Pair, target fixture.Backend, c *sql.Conn) {
	t.Helper()
	const query = "SELECT analyze_count,n_mod_since_analyze FROM pg_catalog.pg_stat_sys_tables WHERE relid='pg_catalog.pg_database'::regclass"
	var before, modified int64
	if err := c.QueryRowContext(p.Ctx, query).Scan(&before, &modified); err != nil {
		t.Fatal(err)
	}
	statsExec(t, p, target, c, "ANALYZE pg_catalog.pg_database", 0)
	fixture.RunStep(t, p.Ctx, c, target.Recorder, fixture.Select("SELECT analyze_count>$1::bigint AS advanced,n_mod_since_analyze FROM pg_catalog.pg_stat_sys_tables WHERE relid='pg_catalog.pg_database'::regclass", []string{"advanced", "n_mod_since_analyze"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(0)}}, before))

	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var workers int64
	for {
		if err := c.QueryRowContext(ctx, "SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE datname=$1 AND backend_type='autovacuum worker'", target.Name).Scan(&workers); err != nil {
			t.Fatalf("database %s setup autovacuum workers last=%d: %v", target.Name, workers, err)
		}
		if workers == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("database %s setup autovacuum workers=%d: %v", target.Name, workers, ctx.Err())
		case <-ticker.C:
		}
	}
}

func statDatabaseWantRetiredDelta(t *testing.T, base, got statDatabaseVector, c, r, i, u, d, s, a, f, k int64) {
	t.Helper()
	want := base
	// Every caller owns all target clients and observes after physical retirement.
	want.Backends = 0
	want.Commit += c
	want.Rollback += r
	want.Insert += i
	want.Update += u
	want.Delete += d
	want.Sessions += s
	want.Abandoned += a
	want.Fatal += f
	want.Killed += k
	if got != want {
		t.Fatalf("database delta: got %+v want %+v (base %+v)", got, want, base)
	}
}
func statDatabasePreparedLabel(prepared bool) string { return fmt.Sprintf("prepared_%t", prepared) }
