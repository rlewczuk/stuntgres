package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var analyzeProgressNames = []string{"pid", "datid", "datname", "relid", "phase", "sample_blks_total", "sample_blks_scanned", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done", "current_child_table_relid", "delay_time"}
var analyzeProgressTypes = []string{"INT4", "OID", "NAME", "OID", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "OID", "FLOAT8"}

const analyzeProgressQuery = "SELECT * FROM pg_catalog.pg_stat_progress_analyze WHERE pid=$1"

func analyzeProgressScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

type analyzeProgressSnapshot struct {
	pid, datid int64
	database   sql.NullString
	relid      sql.NullInt64
	phase      sql.NullString
	counters   [6]sql.NullInt64
	child      sql.NullInt64
	delay      sql.NullFloat64
}

func analyzeProgressScan(t *testing.T, rows *sql.Rows) (analyzeProgressSnapshot, bool) {
	t.Helper()
	var s analyzeProgressSnapshot
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return s, false
	}
	args := []any{&s.pid, &s.datid, &s.database, &s.relid, &s.phase}
	for i := range s.counters {
		args = append(args, &s.counters[i])
	}
	args = append(args, &s.child, &s.delay)
	if err := rows.Scan(args...); err != nil {
		t.Fatal(err)
	}
	return s, true
}

func analyzeProgressRead(t *testing.T, p *fixture.Pair, x fixture.Executor, pid int64) (analyzeProgressSnapshot, bool) {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, analyzeProgressQuery, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	s, ok := analyzeProgressScan(t, rows)
	if rows.Next() {
		t.Fatal("duplicate ANALYZE worker")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return s, ok
}

func analyzeProgressIdle(t *testing.T, p *fixture.Pair, x fixture.Executor, pid int64) {
	t.Helper()
	if s, ok := analyzeProgressRead(t, p, x, pid); ok {
		t.Fatalf("terminal ANALYZE retained: %+v", s)
	}
}

func analyzeProgressIdentity(t *testing.T, s analyzeProgressSnapshot, id copyProgressIdentity) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || !s.database.Valid || s.database.String != id.database {
		t.Fatalf("identity %+v want %+v", s, id)
	}
}

func analyzeProgressVisible(t *testing.T, s analyzeProgressSnapshot, id copyProgressIdentity, pg bool) {
	t.Helper()
	analyzeProgressIdentity(t, s, id)
	if !s.relid.Valid || s.relid.Int64 != id.relid || !s.phase.Valid || s.phase.String != "acquiring inherited sample rows" || !s.child.Valid || s.child.Int64 != 0 || !s.delay.Valid || s.delay.Float64 < 0 {
		t.Fatalf("unfinished inherited wait: %+v", s)
	}
	for i, n := range s.counters {
		if !n.Valid || n.Int64 < 0 || (i >= 2 && n.Int64 != 0) {
			t.Fatalf("authorized zero/retained sample: %+v", s)
		}
	}
	if s.counters[0].Int64 <= 0 || s.counters[1].Int64 != s.counters[0].Int64 {
		t.Fatalf("completed local sample not retained: %+v", s)
	}
	// Only physical sampling differs: this fixture has 129 local input rows.
	if !pg && (s.counters[0].Int64 != 2 || s.delay.Float64 != 0) {
		t.Fatalf("native logical block estimate: %+v", s)
	}
}

func analyzeProgressMasked(t *testing.T, s analyzeProgressSnapshot, id copyProgressIdentity) {
	t.Helper()
	analyzeProgressIdentity(t, s, id)
	if s.relid.Valid || s.phase.Valid || s.child.Valid || s.delay.Valid {
		t.Fatalf("restricted details: %+v", s)
	}
	for _, n := range s.counters {
		if n.Valid {
			t.Fatalf("restricted counter not NULL: %+v", s)
		}
	}
}

func analyzeProgressSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, parent string) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE " + parent + "(a INT,b INT)",
		"CREATE TABLE " + parent + "_child() INHERITS(" + parent + ")",
		"INSERT INTO " + parent + " SELECT i,i%3 FROM generate_series(1,129) i",
		"INSERT INTO " + parent + "_child VALUES(200,2),(201,0)",
	} {
		copyProgressExec(t, p.Ctx, c, q)
	}
}

type analyzeProgressRun struct {
	cancel   context.CancelFunc
	join     func() fixture.RelationOutcome
	finished chan struct{}
	joined   bool
}

func analyzeProgressStart(t *testing.T, p *fixture.Pair, worker *sql.Conn, q string, prepared bool) *analyzeProgressRun {
	t.Helper()
	var stmt *sql.Stmt
	if prepared {
		stmt = vacuumHeld(t, p, worker, q).Statement
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	r := &analyzeProgressRun{cancel: cancel, finished: make(chan struct{})}
	r.join = fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(r.finished)
		var out fixture.RelationOutcome
		if stmt != nil {
			out.Result, out.Err = stmt.ExecContext(ctx)
		} else {
			out.Result, out.Err = worker.ExecContext(ctx, q)
		}
		return out
	})
	t.Cleanup(cancel)
	return r
}

func (r *analyzeProgressRun) live(t *testing.T) {
	t.Helper()
	select {
	case <-r.finished:
		t.Fatal("ANALYZE completed before live observation")
	default:
	}
}

func (r *analyzeProgressRun) result(t *testing.T) error {
	t.Helper()
	if r.joined {
		t.Fatal("ANALYZE joined twice")
	}
	out := r.join()
	r.joined = true
	if out.Err != nil {
		if out.Result != nil {
			t.Fatal("failed ANALYZE published result")
		}
		return out.Err
	}
	if out.Result == nil {
		t.Fatal("successful ANALYZE has no result")
	}
	if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("ANALYZE affected %d: %v", n, err)
	}
	if _, err := out.Result.LastInsertId(); err == nil {
		t.Fatal("ANALYZE exposed LastInsertId")
	}
	return nil
}

func analyzeProgressHold(t *testing.T, p *fixture.Pair, b fixture.Backend, worker *sql.Conn, parent string, prepared bool) (copyProgressIdentity, *sql.Conn, *analyzeProgressRun) {
	t.Helper()
	holder := fixture.ConcurrentIndexConn(t, p, b)
	id := copyProgressIdentify(t, p, worker, parent)
	copyProgressExec(t, p.Ctx, holder, "BEGIN")
	copyProgressExec(t, p.Ctx, holder, "LOCK TABLE ONLY "+parent+"_child IN ACCESS EXCLUSIVE MODE")
	holderPID := fixture.LockPID(t, p.Ctx, holder)
	child := copyProgressIdentify(t, p, holder, parent+"_child").relid
	r := analyzeProgressStart(t, p, worker, "ANALYZE ONLY "+parent, prepared)
	analyzeProgressChildWait(t, p, b, id, holderPID, child, r)
	return id, holder, r
}

func analyzeProgressChildWait(t *testing.T, p *fixture.Pair, b fixture.Backend, id copyProgressIdentity, holderPID int, child int64, r *analyzeProgressRun) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		r.live(t)
		// Another database's held ANALYZE has the same native scheduler
		// frames (and can have the same child OID). Only this exact worker's
		// real child queue entry establishes the inherited-work barrier.
		// Query its own database: native lock captures are database-local.
		var pending bool
		if err := b.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND database=$2::oid AND locktype='relation' AND relation=$3::oid AND mode='AccessShareLock' AND NOT granted) AND $4=ANY(pg_catalog.pg_blocking_pids($1))`, id.pid, id.datid, child, holderPID).Scan(&pending); err != nil {
			t.Fatalf("ANALYZE child wait pid=%d database=%d child=%d holder=%d: %v", id.pid, id.datid, child, holderPID, err)
		}
		r.live(t)
		if pending {
			return
		}
		select {
		case <-r.finished:
			t.Fatal("ANALYZE completed before required child lock wait")
		case <-ctx.Done():
			t.Fatalf("ANALYZE child wait pid=%d database=%d child=%d holder=%d: %v", id.pid, id.datid, child, holderPID, ctx.Err())
		case <-tick.C:
		}
	}
}

func analyzeProgressObserve(t *testing.T, p *fixture.Pair, x fixture.Executor, id copyProgressIdentity, r *analyzeProgressRun) analyzeProgressSnapshot {
	t.Helper()
	r.live(t)
	s, ok := analyzeProgressRead(t, p, x, id.pid)
	if !ok {
		t.Fatal("missing admitted ANALYZE")
	}
	r.live(t)
	return s
}

func analyzeProgressCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn, id copyProgressIdentity, r *analyzeProgressRun) {
	t.Helper()
	r.live(t)
	if b.PG {
		var ok bool
		if err := admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", id.pid).Scan(&ok); err != nil || !ok {
			t.Fatalf("cancel=%v: %v", ok, err)
		}
	} else {
		r.cancel()
	}
	err := r.result(t)
	if b.PG {
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, err, &want)
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	analyzeProgressIdle(t, p, admin, id.pid)
}
