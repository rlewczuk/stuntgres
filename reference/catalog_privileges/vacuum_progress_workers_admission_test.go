package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// The retained lock is on each real index, after heap admission and progress
// publication. SET TABLESPACE to the effective current space does not move it.
type vacuumProgressIndexMetadata struct {
	oid, tablespace, filenode int64
	name, space               string
	valid, ready              bool
}

type vacuumProgressIndexBlocker struct {
	conn   *sql.Conn
	tx     *sql.Tx
	pid    int
	table  string
	before [2]vacuumProgressIndexMetadata
}

func vacuumProgressIndexes(t *testing.T, ctx context.Context, c fixture.Executor, table string) [2]vacuumProgressIndexMetadata {
	t.Helper()
	rows, err := c.QueryContext(ctx, `SELECT c.oid,c.relname,c.reltablespace,c.relfilenode,i.indisvalid,i.indisready,s.spcname
FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid
JOIN pg_catalog.pg_database d ON d.datname=current_database()
JOIN pg_catalog.pg_tablespace s ON s.oid=CASE WHEN c.reltablespace=0 THEN d.dattablespace ELSE c.reltablespace END
WHERE i.indrelid=$1::regclass ORDER BY c.relname`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [2]vacuumProgressIndexMetadata
	n := 0
	for rows.Next() {
		if n == len(result) {
			t.Fatal("unexpected additional index", table)
		}
		m := &result[n]
		if err := rows.Scan(&m.oid, &m.name, &m.tablespace, &m.filenode, &m.valid, &m.ready, &m.space); err != nil {
			t.Fatal(err)
		}
		want := table + "_id"
		if n == 1 {
			want = table + "_payload"
		}
		if m.name != want || m.oid <= 0 || m.filenode <= 0 || m.space == "" || !m.valid || !m.ready {
			t.Fatalf("invalid real index metadata: %+v want %s", m, want)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 || result[0].oid == result[1].oid {
		t.Fatalf("expected two distinct actual indexes: %+v", result)
	}
	return result
}

func vacuumProgressBlockIndexes(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, table string) *vacuumProgressIndexBlocker {
	t.Helper()
	h := &vacuumProgressIndexBlocker{table: table, before: vacuumProgressIndexes(t, p.Ctx, observer, table)}
	h.conn = vacuumConn(t, p, b)
	h.pid = fixture.LockPID(t, p.Ctx, h.conn)
	// Register rollback before acquisition; this runs before borrower closure.
	t.Cleanup(func() { h.release(t) })
	return h
}

func (h *vacuumProgressIndexBlocker) acquire(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var err error
	h.tx, err = h.conn.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range h.before {
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		if _, err := h.tx.ExecContext(p.Ctx, "ALTER INDEX "+quote(m.name)+" SET TABLESPACE "+quote(m.space)); err != nil {
			t.Fatal(err)
		}
	}
	h.unchanged(t, p)
	vacuumProgressSurvivors(t, p, b, h.tx, h.table)
}

func (h *vacuumProgressIndexBlocker) release(t *testing.T) {
	t.Helper()
	if h.tx != nil {
		if err := h.tx.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Error(err)
		}
		h.tx = nil
	}
}

func (h *vacuumProgressIndexBlocker) unchanged(t *testing.T, p *fixture.Pair) {
	t.Helper()
	if got := vacuumProgressIndexes(t, p.Ctx, h.tx, h.table); got != h.before {
		t.Fatalf("same-tablespace ALTER moved/changed indexes: got %+v want %+v", got, h.before)
	}
}

func (h *vacuumProgressIndexBlocker) unchangedAfterRelease(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	if got := vacuumProgressIndexes(t, p.Ctx, c, h.table); got != h.before {
		t.Fatalf("blocker rollback changed indexes: got %+v want %+v", got, h.before)
	}
}

func (h *vacuumProgressIndexBlocker) admitted(t *testing.T, p *fixture.Pair, observer *sql.Conn, id copyProgressIdentity, r *vacuumProgressRunning) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	var waiting int64
	for waiting == 0 {
		vacuumProgressUnfinished(t, r)
		err := observer.QueryRowContext(ctx, `SELECT relation FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='relation' AND mode='RowExclusiveLock' AND NOT granted AND relation IN ($2,$3)`, id.pid, h.before[0].oid, h.before[1].oid).Scan(&waiting)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatal("no actual index-open waiter", ctx.Err())
		}
		runtime.Gosched()
	}
	// No completion escape: actual queued lock and blocker membership required.
	fixture.LockWaitBehind(t, ctx, int(id.pid), h.pid, waiting, "RowExclusiveLock")
	var granted bool
	if err := observer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$2 AND mode='AccessExclusiveLock' AND granted)`, h.pid, waiting).Scan(&granted); err != nil || !granted {
		t.Fatalf("own blocker AX grant on waiting index %d: granted=%v err=%v", waiting, granted, err)
	}
	s, ok := vacuumProgressScan(t, observer.QueryRowContext(ctx, vacuumProgressQuery, id.pid))
	if !ok {
		t.Fatal("index waiter has no genuine heap progress", id)
	}
	vacuumProgressVisible(t, s, id, true)
	vacuumProgressIndexOpen(t, s)
	vacuumProgressUnfinished(t, r)
}

func vacuumProgressIndexOpen(t *testing.T, s vacuumProgressSnapshot) {
	t.Helper()
	if s.phase.String != "initializing" || !s.phase.Valid || !s.delay.Valid || s.delay.Float64 != 0 {
		t.Fatalf("index-open progress must initialize with zero delay: %+v", s)
	}
	for _, n := range s.counters {
		if !n.Valid || n.Int64 != 0 {
			t.Fatalf("index-open work counters must all be zero: %+v", s)
		}
	}
}

func vacuumProgressTypes(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns := []string{"pid", "datid", "datname", "relid", "phase", "heap_blks_total", "heap_blks_scanned", "heap_blks_vacuumed", "index_vacuum_count", "max_dead_tuple_bytes", "dead_tuple_bytes", "num_dead_item_ids", "indexes_total", "indexes_processed", "delay_time"}
	types := []string{"INT4", "OID", "NAME", "OID", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8"}
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, columns) {
		t.Fatalf("progress columns=%v want %v err=%v", got, columns, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != len(types) {
		t.Fatalf("progress type count=%d want %d", len(metadata), len(types))
	}
	for i, column := range metadata {
		if column.DatabaseTypeName() != types[i] {
			t.Fatalf("progress column %s type=%s want %s", columns[i], column.DatabaseTypeName(), types[i])
		}
	}
}

func vacuumProgressFreshWorker(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, held *sql.Stmt, id copyProgressIdentity, r *vacuumProgressRunning) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		vacuumProgressUnfinished(t, r)
		var rows *sql.Rows
		var err error
		if prepared {
			rows, err = held.QueryContext(p.Ctx, id.pid)
		} else {
			rows, err = observer.QueryContext(p.Ctx, vacuumProgressQuery, id.pid)
		}
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			vacuumProgressTypes(t, rows)
			if !rows.Next() {
				t.Fatal("surviving worker missing", rows.Err())
			}
			s, _ := vacuumProgressScan(t, rows)
			vacuumProgressVisible(t, s, id, b.PG)
			if b.PG {
				vacuumProgressIndexOpen(t, s)
			} else if !vacuumProgressNativeStack(r.workerStack) {
				t.Fatal("surviving worker lost actual storage frame")
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("surviving worker must have exactly one row", rows.Err())
			}
		}()
		vacuumProgressUnfinished(t, r)
	}
}

func vacuumProgressNotCompleted(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	vacuumCount(t, p.Ctx, c, table, 0)
	vacuumRun(t, p, b, c, fixture.Select("SELECT vacuum_count,last_vacuum IS NULL AS never_completed FROM pg_stat_all_tables WHERE relid='"+table+"'::regclass", []string{"vacuum_count", "never_completed"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}))
}

func vacuumProgressSurvivors(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(vacuumProgressVersions / 2)}}))
	vacuumRun(t, p, b, c, fixture.Select("SELECT id,payload FROM "+table+" WHERE id IN (0,1,2,3,262141,262142,262143) ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "v"}, {int64(3), "v"}, {int64(262141), "v"}, {int64(262143), "v"}}))
	vacuumRun(t, p, b, c, fixture.Select("SELECT indexrelid='"+table+"_id'::regclass AS id_index,indexrelid='"+table+"_payload'::regclass AS payload_index,indisvalid,indisready FROM pg_index WHERE indrelid='"+table+"'::regclass ORDER BY id_index DESC", []string{"id_index", "payload_index", "indisvalid", "indisready"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true}, {false, true, true, true}}))
}
