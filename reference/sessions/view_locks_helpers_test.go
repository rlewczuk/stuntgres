package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var viewLockColumns = []string{"locktype", "database", "relation", "page", "tuple", "virtualxid", "transactionid", "classid", "objid", "objsubid", "virtualtransaction", "pid", "mode", "granted", "fastpath", "waitstart"}
var viewLockTypes = []string{"TEXT", "OID", "OID", "INT4", "INT2", "TEXT", "XID", "OID", "OID", "INT2", "TEXT", "INT4", "TEXT", "BOOL", "BOOL", "TIMESTAMPTZ"}

func viewLocksScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

type viewLockRow struct {
	Kind                            string
	Database, Relation, Page, Tuple sql.NullInt64
	VirtualXID, XID                 sql.NullString
	Class, Object, Subobject        sql.NullInt64
	Owner                           sql.NullString
	PID                             sql.NullInt64
	Mode                            string
	Granted, Fast                   bool
	Wait                            sql.NullTime
}

func viewLocksQuery(where string) string {
	return "SELECT " + strings.Join(viewLockColumns, ",") + " FROM pg_catalog.pg_locks WHERE " + where
}

func viewLocksMetadata(t *testing.T, r *sql.Rows, pg bool) {
	t.Helper()
	cols, err := r.Columns()
	if err != nil || !slices.Equal(cols, viewLockColumns) {
		t.Fatal("pg_locks columns", cols, err)
	}
	meta, err := r.ColumnTypes()
	if err != nil || len(meta) != 16 {
		t.Fatal("pg_locks metadata", meta, err)
	}
	for i, m := range meta {
		if m.DatabaseTypeName() != viewLockTypes[i] {
			t.Fatalf("column %s type %s want %s", cols[i], m.DatabaseTypeName(), viewLockTypes[i])
		}
		n, known := m.Nullable()
		if pg && (n || known) || !pg && (!n || !known) {
			t.Fatal("nullability", cols[i], n, known)
		}
		if !pg {
			want := reflect.TypeFor[string]()
			switch i {
			case 1, 2, 3, 4, 7, 8, 9, 11:
				want = reflect.TypeFor[int64]()
			case 6:
				want = reflect.TypeFor[[]byte]()
			case 13, 14:
				want = reflect.TypeFor[bool]()
			case 15:
				want = reflect.TypeFor[time.Time]()
			}
			if m.ScanType() != want {
				t.Fatal("native ScanType", cols[i], m.ScanType(), want)
			}
		}
		if pg {
			want := reflect.TypeFor[string]()
			switch i {
			case 1, 2, 6, 7, 8:
				want = reflect.TypeFor[any]()
			case 3, 11:
				want = reflect.TypeFor[int32]()
			case 4, 9:
				want = reflect.TypeFor[int16]()
			case 13, 14:
				want = reflect.TypeFor[bool]()
			case 15:
				want = reflect.TypeFor[time.Time]()
			}
			if m.ScanType() != want {
				t.Fatal("pq ScanType", cols[i], m.ScanType(), want)
			}
		}
	}
}

func viewLocksRead(t *testing.T, ctx context.Context, b fixture.Backend, x fixture.Executor, where string, args ...any) []viewLockRow {
	t.Helper()
	r, err := x.QueryContext(ctx, viewLocksQuery(where), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	viewLocksMetadata(t, r, b.PG)
	var result []viewLockRow
	for r.Next() {
		var v viewLockRow
		if err := r.Scan(&v.Kind, &v.Database, &v.Relation, &v.Page, &v.Tuple, &v.VirtualXID, &v.XID, &v.Class, &v.Object, &v.Subobject, &v.Owner, &v.PID, &v.Mode, &v.Granted, &v.Fast, &v.Wait); err != nil {
			t.Fatal(err)
		}
		if !v.Owner.Valid || v.Owner.String == "" || (!b.PG && v.Fast) || (v.Granted && v.Wait.Valid) {
			t.Fatalf("invalid ownership/mechanism/lifetime: %#v", v)
		}
		result = append(result, v)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

func viewLocksPoll(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, where string, match func([]viewLockRow) bool, args ...any) []viewLockRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 4*time.Second)
	defer cancel()
	for {
		got := viewLocksRead(t, ctx, b, x, where, args...)
		if match(got) {
			return got
		}
		if ctx.Err() != nil {
			t.Fatalf("lock admission: last=%#v: %v", got, ctx.Err())
		}
		runtime.Gosched()
	}
}

func viewLocksOne(t *testing.T, rows []viewLockRow) viewLockRow {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("want one lock, got %#v", rows)
	}
	return rows[0]
}

func viewLocksEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, where string, args ...any) {
	t.Helper()
	if rows := viewLocksRead(t, p.Ctx, b, x, where, args...); len(rows) != 0 {
		t.Fatalf("unexpected locks %#v", rows)
	}
}

func viewLocksOID(t *testing.T, p *fixture.Pair, x fixture.Executor, query string, args ...any) int64 {
	t.Helper()
	var oid int64
	viewLocksScalar(t, p, x, query, &oid, args...)
	if oid <= 0 || oid > 4294967295 {
		t.Fatal("catalog identity", oid)
	}
	return oid
}

func viewLocksScalar(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, dest any, args ...any) {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing scalar row", q, rows.Err())
	}
	if err := rows.Scan(dest); err != nil {
		t.Fatal(q, err)
	}
	if rows.Next() {
		t.Fatal("extra scalar row", q)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(q, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(q, err)
	}
}

func viewLocksDatabase(t *testing.T, p *fixture.Pair, x fixture.Executor) int64 {
	t.Helper()
	return viewLocksOID(t, p, x, "SELECT oid FROM pg_database WHERE datname=current_database()")
}

func viewLocksTarget(t *testing.T, row viewLockRow, kind string, database, relation int64) {
	t.Helper()
	if row.Kind != kind || row.Database != (sql.NullInt64{Int64: database, Valid: database != 0}) || row.Relation != (sql.NullInt64{Int64: relation, Valid: relation != 0}) || row.Page.Valid || row.Tuple.Valid {
		t.Fatalf("wrong target %#v", row)
	}
	if kind != "virtualxid" && row.VirtualXID.Valid || kind != "transactionid" && row.XID.Valid {
		t.Fatalf("unrelated transaction target %#v", row)
	}
	if kind != "object" && kind != "advisory" && (row.Class.Valid || row.Object.Valid || row.Subobject.Valid) {
		t.Fatalf("unrelated object target %#v", row)
	}
}

func viewLocksPending(t *testing.T, rows []viewLockRow, pid int, mode string, before time.Time) viewLockRow {
	t.Helper()
	for _, r := range rows {
		if r.PID.Valid && r.PID.Int64 == int64(pid) && r.Mode == mode && !r.Granted {
			if !r.Wait.Valid || r.Wait.Time.Before(before.Add(-time.Second)) || r.Wait.Time.After(time.Now().Add(time.Second)) {
				t.Fatalf("wait timestamp outside enqueue interval: %#v before=%v", r, before)
			}
			return r
		}
	}
	t.Fatalf("missing pending %d/%s: %#v", pid, mode, rows)
	return viewLockRow{}
}

func viewLocksHasPending(rows []viewLockRow, pid int, mode string) bool {
	for _, r := range rows {
		if r.PID.Valid && r.PID.Int64 == int64(pid) && r.Mode == mode && !r.Granted && r.Wait.Valid {
			return true
		}
	}
	return false
}

func viewLocksOwner(t *testing.T, p *fixture.Pair, b fixture.Backend, observer fixture.Executor, pid int) viewLockRow {
	t.Helper()
	r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, observer, "pid=$1 AND locktype='virtualxid' AND granted", pid))
	viewLocksTarget(t, r, "virtualxid", 0, 0)
	if r.Mode != "ExclusiveLock" || r.VirtualXID != r.Owner {
		t.Fatal("VXID ownership", r)
	}
	return r
}

func viewLocksXID(t *testing.T, p *fixture.Pair, b fixture.Backend, observer fixture.Executor, pid int) viewLockRow {
	t.Helper()
	r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, observer, "pid=$1 AND locktype='transactionid' AND granted", pid))
	viewLocksTarget(t, r, "transactionid", 0, 0)
	if r.Mode != "ExclusiveLock" || !r.XID.Valid || r.XID.String == "0" {
		t.Fatal("XID ownership", r)
	}
	return r
}
