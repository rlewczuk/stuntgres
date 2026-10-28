package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func detachConcurrentScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func detachConcurrentSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE dc_parent(k INT,payload TEXT,CONSTRAINT dc_positive CHECK(k>0)) PARTITION BY RANGE(k)",
		"CREATE TABLE dc_keep PARTITION OF dc_parent FOR VALUES FROM(1) TO(2)",
		"CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES FROM(2) TO(4)",
		"CREATE UNIQUE INDEX dc_key ON dc_parent(k)",
		"CREATE UNIQUE INDEX dc_payload ON dc_parent(k,payload)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO dc_parent VALUES(1,'keep'),(2,'leave')", 2))
}

func detachConcurrentRows(table string, values ...int64) fixture.Step {
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	return fixture.Select("SELECT k FROM "+table+" ORDER BY k", []string{"k"}, []string{"INT4"}, rows)
}

func detachConcurrentGraph(parent, child string, attached, pending bool) fixture.Step {
	rows := [][]any(nil)
	if attached {
		rows = [][]any{{pending}}
	}
	return fixture.Select("SELECT inhdetachpending FROM pg_inherits WHERE inhparent='"+parent+"'::regclass AND inhrelid='"+child+"'::regclass", []string{"inhdetachpending"}, []string{"BOOL"}, rows)
}

func detachConcurrentState(run func(fixture.Step), attached, pending bool) {
	run(detachConcurrentGraph("dc_parent", "dc_leave", attached, pending))
	run(fixture.Select("SELECT relispartition,relpartbound IS NULL AS bound_null FROM pg_class WHERE oid='dc_leave'::regclass", []string{"relispartition", "bound_null"}, []string{"BOOL", "BOOL"}, [][]any{{attached, !attached}}))
	run(partitionDetachTopEdges("dc_leave_k_idx", attached))
	run(detachConcurrentRows("dc_leave", 2))
}

func detachConcurrentConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func detachConcurrentPID(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if !b.PG {
		return 0
	}
	return fixture.LockPID(t, p.Ctx, c)
}

type detachConcurrentWorker struct {
	conn     *sql.Conn
	pid      int
	finished chan struct{}
	cancel   context.CancelFunc
	join     func() fixture.RelationOutcome
}

func detachConcurrentStart(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, held bool) *detachConcurrentWorker {
	t.Helper()
	c := detachConcurrentConn(t, p, b)
	w := &detachConcurrentWorker{conn: c, pid: detachConcurrentPID(t, p, b, c), finished: make(chan struct{})}
	var stmt *sql.Stmt
	if held {
		var err error
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("held utility prepare", err)
		}
		t.Cleanup(func() { stmt.Close() })
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	w.cancel = cancel
	t.Cleanup(cancel)
	w.join = fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(w.finished)
		var r sql.Result
		var err error
		if stmt != nil {
			r, err = stmt.ExecContext(ctx)
		} else {
			r, err = c.ExecContext(ctx, q)
		}
		return fixture.RelationOutcome{Result: r, Err: err}
	})
	return w
}

func detachConcurrentCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, w *detachConcurrentWorker) {
	t.Helper()
	if b.PG {
		var ok bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", w.pid).Scan(&ok); err != nil || !ok {
			t.Fatal("server cancellation", ok, err)
		}
	} else {
		w.cancel()
	}
	o := w.join()
	if o.Result != nil || o.Err == nil {
		t.Fatal("canceled utility returned success", o)
	}
	if b.PG {
		fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else if !errors.Is(o.Err, context.Canceled) {
		t.Fatal("native caller cancellation", o.Err)
	}
	fixture.RunStep(t, p.Ctx, w.conn, b.Recorder, fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func detachConcurrentSuccess(t *testing.T, o fixture.RelationOutcome) {
	t.Helper()
	if o.Err != nil || o.Result == nil {
		t.Fatal("utility completion", o.Err)
	}
	n, err := o.Result.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal("DDL affected count", n, err)
	}
	if _, err = o.Result.LastInsertId(); err == nil {
		t.Fatal("DDL supports LastInsertId")
	}
}

func detachConcurrentPending(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
	t.Helper()
	detachConcurrentPendingRelations(t, p, b, "dc_parent", "dc_leave", held)
}

func detachConcurrentPendingRelations(t *testing.T, p *fixture.Pair, b fixture.Backend, parent, child string, held bool) {
	t.Helper()
	c := detachConcurrentConn(t, p, b)
	pid := detachConcurrentPID(t, p, b, c)
	tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows(parent, 1, 2))
	w := detachConcurrentStart(t, p, b, "ALTER TABLE "+parent+" DETACH PARTITION "+child+" CONCURRENTLY", held)
	fixture.PartitionDetachPending(t, p, b, parent, child, w.pid, pid, w.finished)
	detachConcurrentCancel(t, p, b, w)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

const detachConcurrentHint = "Use ALTER TABLE ... DETACH PARTITION ... FINALIZE to complete the pending detach operation."

func detachConcurrentExecute(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, held bool) {
	t.Helper()
	if !held {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
		return
	}
	stmt, err := e.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
}

// Unlike partitionConstraintIndexState, this witness is for an ordinary
// nonunique index. Keep all four independent index flags explicit.
func detachConcurrentPlainIndex(name string) fixture.Step {
	return fixture.Select("SELECT indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='"+name+"'::regclass::OID",
		[]string{"indisunique", "indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true}})
}
