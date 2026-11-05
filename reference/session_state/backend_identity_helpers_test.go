package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var identityFunctions = []string{"pg_stat_get_backend_pid", "pg_stat_get_backend_dbid", "pg_stat_get_backend_userid", "pg_stat_get_backend_start", "pg_stat_get_backend_client_addr", "pg_stat_get_backend_client_port"}
var identityTypes = []string{"INT4", "OID", "OID", "TIMESTAMPTZ", "INET", "INT4"}
var identityNames = []string{"id", "pid", "dbid", "userid", "started", "addr", "port"}
var identityTupleTypes = []string{"INT4", "INT4", "OID", "OID", "TIMESTAMPTZ", "INET", "INT4"}

const identityLookupSQL = "SELECT $1::integer AS id,pg_stat_get_backend_pid($1) AS pid,pg_stat_get_backend_dbid($1) AS dbid,pg_stat_get_backend_userid($1) AS userid,pg_stat_get_backend_start($1) AS started,pg_stat_get_backend_client_addr($1) AS addr,pg_stat_get_backend_client_port($1) AS port"
const identitySetSQL = "SELECT id,pg_stat_get_backend_pid(id) AS pid,pg_stat_get_backend_dbid(id) AS dbid,pg_stat_get_backend_userid(id) AS userid,pg_stat_get_backend_start(id) AS started,pg_stat_get_backend_client_addr(id) AS addr,pg_stat_get_backend_client_port(id) AS port FROM pg_stat_get_backend_idset() AS b(id)"

func identityMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(types) {
		t.Fatal("identity metadata", columns, err)
	}
	for i, c := range columns {
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
			t.Fatal("identity declared metadata", i, c.Name(), c.DatabaseTypeName(), names, types)
		}
		if _, known := c.Nullable(); known {
			t.Fatal("computed identity nullability must be unknown", i)
		}
		want := reflect.TypeOf(int64(0))
		switch types[i] {
		case "INT4":
			if pg {
				want = reflect.TypeOf(int32(0))
			}
		case "TIMESTAMPTZ":
			want = reflect.TypeOf(time.Time{})
		case "INET":
			want = reflect.TypeOf("")
			if pg {
				want = reflect.TypeFor[any]()
			}
		case "OID":
			if pg {
				want = reflect.TypeFor[any]()
			}
		}
		if c.ScanType() != want {
			t.Fatalf("identity %s scan type %v want %v", names[i], c.ScanType(), want)
		}
	}
}

func identityRows(t *testing.T, rows *sql.Rows, pg bool, names, types []string) [][]any {
	t.Helper()
	defer rows.Close()
	identityMetadata(t, rows, pg, names, types)
	return backendRows(t, rows, pg, names, types)
}

func identityQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, args ...any) [][]any {
	t.Helper()
	r, err := e.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	return identityRows(t, r, b.PG, identityNames, identityTupleTypes)
}

func identitySet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) [][]any {
	t.Helper()
	rows := identityQuery(t, p, b, e, identitySetSQL)
	seen := map[int64]bool{}
	for _, row := range rows {
		id, ok := row[0].(int64)
		if !ok || id < 0 || id > 2147483647 || seen[id] {
			t.Fatal("unique nonnegative INT4 slots", rows)
		}
		seen[id] = true
	}
	return rows
}

func identityForPID(t *testing.T, rows [][]any, pid int64) []any {
	t.Helper()
	var found []any
	for _, row := range rows {
		if row[1] == pid {
			if found != nil {
				t.Fatal("duplicate PID mapping", pid, rows)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatal("missing independently known PID", pid, rows)
	}
	return found
}

func identityLookup(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, id any) []any {
	t.Helper()
	rows := identityQuery(t, p, b, e, identityLookupSQL, id)
	if len(rows) != 1 {
		t.Fatal("scalar tuple cardinality", rows)
	}
	return rows[0]
}

func identityEqual(t *testing.T, got, want []any) {
	t.Helper()
	for i := range want {
		if !reflect.DeepEqual(fixture.Normalize(got[i]), fixture.Normalize(want[i])) {
			t.Fatalf("identity field %s got %T %v want %T %v", identityNames[i], got[i], got[i], want[i], want[i])
		}
	}
}

func identityMapped(t *testing.T, p *fixture.Pair, b fixture.Backend, o, peer *sql.Conn) []any {
	t.Helper()
	pid := statActivityPID(t, p, peer)
	var dbid, userid int64
	if err := peer.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()").Scan(&dbid); err != nil {
		t.Fatal(err)
	}
	if err := peer.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=session_user").Scan(&userid); err != nil {
		t.Fatal(err)
	}
	activity := statActivityGet(t, p, o, pid, true)
	row := identityForPID(t, identitySet(t, p, b, o), pid)
	if fmt.Sprint(row[2]) != fmt.Sprint(dbid) || fmt.Sprint(row[3]) != fmt.Sprint(userid) {
		t.Fatal("independent database/login mapping", row, dbid, userid)
	}
	if stamp, ok := row[4].(time.Time); !ok || stamp.IsZero() || !stamp.Equal(activity.stamp(t, 10)) {
		t.Fatal("independent backend birth", row[4], activity[10])
	}
	if fmt.Sprint(row[5]) != fmt.Sprint(fixture.Normalize(activity[7])) || fmt.Sprint(row[6]) != fmt.Sprint(activity[9]) {
		t.Fatal("actual endpoint mapping", row, activity)
	}
	if !b.PG && (row[5] != nil || row[6] != nil) {
		t.Fatal("invented in-process endpoint", row)
	}
	if b.PG && (row[5] == nil || row[6] == nil || row[6].(int64) <= 0) {
		t.Fatal("real TCP endpoint witness", row)
	}
	return row
}

func identityMasked(t *testing.T, got, visible []any, allowed bool) {
	t.Helper()
	identityEqual(t, got[:4], visible[:4])
	for i := 4; i < 7; i++ {
		if allowed {
			if !reflect.DeepEqual(fixture.Normalize(got[i]), fixture.Normalize(visible[i])) {
				t.Fatal("current inherited protected fact", i, got, visible)
			}
		} else if got[i] != nil {
			t.Fatal("unauthorized protected fact", i, got)
		}
	}
}

// A retired slot may have a real replacement. Never treat recyclable IDs as PIDs.
func identityRetiredFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn, old []any) {
	t.Helper()
	rows := identitySet(t, p, b, o)
	for _, row := range rows {
		if row[1] == old[1] {
			t.Fatal("retired PID in fresh cut", row)
		}
		if row[0] == old[0] {
			activity := statActivityGet(t, p, o, row[1].(int64), false)
			if fmt.Sprint(row[2]) != fmt.Sprint(fixture.Normalize(activity[0])) || fmt.Sprint(row[3]) != fmt.Sprint(fixture.Normalize(activity[4])) {
				t.Fatal("reused slot not a proven replacement", row, activity)
			}
			identityEqual(t, identityLookup(t, p, b, o, old[0]), row)
			return
		}
	}
	got := identityLookup(t, p, b, o, old[0])
	for _, value := range got[1:] {
		if value != nil {
			t.Fatal("unrepresented retired ID must be unavailable", got)
		}
	}
}
