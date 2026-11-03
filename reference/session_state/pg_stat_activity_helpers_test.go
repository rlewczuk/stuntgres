package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var statActivityNames = []string{"datid", "datname", "pid", "leader_pid", "usesysid", "usename", "application_name", "client_addr", "client_hostname", "client_port", "backend_start", "xact_start", "query_start", "state_change", "wait_event_type", "wait_event", "state", "backend_xid", "backend_xmin", "query_id", "query", "backend_type"}
var statActivityTypes = []string{"OID", "NAME", "INT4", "INT4", "OID", "NAME", "TEXT", "INET", "TEXT", "INT4", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TEXT", "TEXT", "TEXT", "XID", "XID", "INT8", "TEXT", "TEXT"}

const statActivitySelect = "SELECT * FROM pg_catalog.pg_stat_activity WHERE pid=$1"

type statActivityRow [22]any

func (r statActivityRow) text(i int) string {
	if r[i] == nil {
		return "<NULL>"
	}
	if b, ok := r[i].([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(r[i])
}
func (r statActivityRow) stamp(t *testing.T, i int) time.Time {
	t.Helper()
	v, ok := r[i].(time.Time)
	if !ok {
		t.Fatalf("column %s timestamp %T %v", statActivityNames[i], r[i], r[i])
	}
	return v
}
func statActivityConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func statActivityPID(t *testing.T, p *fixture.Pair, c *sql.Conn) int64 {
	t.Helper()
	var pid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}
func statActivityClear(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	activityExec(t, p, c, statsClearSQL)
}
func statActivityRead(t *testing.T, rows *sql.Rows) (statActivityRow, bool) {
	t.Helper()
	defer rows.Close()
	var row statActivityRow
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return row, false
	}
	targets := make([]any, 22)
	for i := range row {
		targets[i] = &row[i]
	}
	if err := rows.Scan(targets...); err != nil {
		t.Fatal(err)
	}
	// Own binary scalars even after driver Rows.Close.
	for i, v := range row {
		if data, ok := v.([]byte); ok {
			row[i] = append([]byte(nil), data...)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("activity duplicate/error", rows.Err())
	}
	return row, true
}
func statActivityGet(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64, fresh bool) statActivityRow {
	t.Helper()
	if fresh {
		statActivityClear(t, p, c)
	}
	rows, err := c.QueryContext(p.Ctx, statActivitySelect, pid)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := statActivityRead(t, rows)
	if !ok {
		t.Fatalf("missing activity %d", pid)
	}
	return row
}
func statActivityAbsent(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64) {
	t.Helper()
	statActivityClear(t, p, c)
	rows, err := c.QueryContext(p.Ctx, statActivitySelect, pid)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := statActivityRead(t, rows)
	if ok {
		t.Fatalf("retired backend %d remains", pid)
	}
}
func statActivityRetired(t *testing.T, p *fixture.Pair, observer *sql.Conn, pid int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	for {
		statActivityClear(t, p, observer)
		var count int
		if err := observer.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("backend retirement", ctx.Err())
		}
		runtime.Gosched()
	}
}
func statActivityMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, statActivityNames) {
		t.Fatalf("columns %v", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != statActivityTypes[i] {
			t.Fatalf("%s type %s", names[i], typ.DatabaseTypeName())
		}
		if !pg {
			if nullable, known := typ.Nullable(); !nullable || !known {
				t.Fatalf("%s nullable %t known %t", names[i], nullable, known)
			}
			want := reflect.TypeOf("")
			switch i {
			case 0, 2, 3, 4, 9, 19:
				want = reflect.TypeOf(int64(0))
			case 10, 11, 12, 13:
				want = reflect.TypeOf(time.Time{})
			case 17, 18:
				want = reflect.TypeOf([]byte{})
			}
			if typ.ScanType() != want {
				t.Fatalf("%s scan %v want %v", names[i], typ.ScanType(), want)
			}
		}
	}
}
func statActivityState(t *testing.T, pg bool, r statActivityRow, state, query string) {
	t.Helper()
	if r.text(16) != state || r.text(20) != query {
		t.Fatalf("state/query %q %q want %q %q", r.text(16), r.text(20), state, query)
	}
	statActivityIdleWait(t, pg, r)
}
func statActivityIdleWait(t *testing.T, pg bool, r statActivityRow) {
	t.Helper()
	if pg {
		if r.text(14) != "Client" || r.text(15) != "ClientRead" {
			t.Fatalf("actual TCP idle wait %v %v", r[14], r[15])
		}
	} else if r[14] != nil || r[15] != nil {
		t.Fatalf("invented native idle wait %v %v", r[14], r[15])
	}
}
func statActivityAwait(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64, event string) statActivityRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("activity wait %s: %v", event, err)
		}
		r := statActivityGet(t, p, c, pid, true)
		if r.text(15) == event {
			return r
		}
		runtime.Gosched()
	}
}
func statActivityBlocker(t *testing.T, p *fixture.Pair, c *sql.Conn, waiter, holder int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	for {
		var got string
		if err := c.QueryRowContext(ctx, "SELECT pg_blocking_pids($1)::text", waiter).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got == fmt.Sprintf("{%d}", holder) {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("blocker barrier", ctx.Err(), got)
		}
		runtime.Gosched()
	}
}
func statActivitySignal(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64, terminate bool) {
	t.Helper()
	name := "pg_cancel_backend"
	if terminate {
		name = "pg_terminate_backend"
	}
	var ok bool
	if err := c.QueryRowContext(p.Ctx, "SELECT "+name+"($1)", pid).Scan(&ok); err != nil || !ok {
		t.Fatalf("%s: %t %v", name, ok, err)
	}
}
func statActivityDenied(t *testing.T, visible, masked statActivityRow) {
	t.Helper()
	public := map[int]bool{0: true, 1: true, 2: true, 4: true, 5: true, 6: true, 17: true, 18: true}
	for i := range masked {
		if public[i] {
			if masked.text(i) != visible.text(i) {
				t.Fatalf("public %s %v != %v", statActivityNames[i], masked[i], visible[i])
			}
		} else if i == 20 {
			if masked.text(i) != "<insufficient privilege>" {
				t.Fatal("query redaction", masked[i])
			}
		} else if masked[i] != nil {
			t.Fatalf("protected %s leaked %v", statActivityNames[i], masked[i])
		}
	}
}
