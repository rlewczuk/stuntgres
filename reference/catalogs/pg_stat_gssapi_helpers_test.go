package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var gssapiNames = []string{"pid", "gss_authenticated", "principal", "encrypted", "credentials_delegated"}
var gssapiTypes = []string{"INT4", "BOOL", "TEXT", "BOOL", "BOOL"}

const gssapiQuery = "SELECT * FROM pg_catalog.pg_stat_gssapi"
const gssapiPIDQuery = gssapiQuery + " WHERE pid=$1"

func gssapiMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, gssapiNames) {
		t.Fatalf("GSSAPI columns=%v error=%v", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(gssapiTypes) {
		t.Fatalf("GSSAPI metadata=%v error=%v", types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != gssapiTypes[i] {
			t.Fatalf("column %d type=%s want %s", i, typ.DatabaseTypeName(), gssapiTypes[i])
		}
		if !pg {
			nullable, known := typ.Nullable()
			if !known || !nullable {
				t.Fatalf("view attribute %d nullable=%v known=%v", i, nullable, known)
			}
		}
	}
}

func gssapiTuples(pids ...int64) [][]any {
	rows := make([][]any, 0, len(pids))
	for _, pid := range pids {
		rows = append(rows, []any{pid, false, nil, false, false})
	}
	return rows
}

// Login connectors deliberately use the public unwrapped role fixture. Validate
// their actual raw cells as well as normalized values, without inventing recorder
// events for a connection that is not middleware-wrapped.
func gssapiConsume(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	gssapiMetadata(t, rows, pg)
	got := make([][]any, 0)
	for rows.Next() {
		var pid, authenticated, principal, encrypted, delegated any
		if err := rows.Scan(&pid, &authenticated, &principal, &encrypted, &delegated); err != nil {
			t.Fatal(err)
		}
		values := []any{pid, authenticated, principal, encrypted, delegated}
		for i, value := range values {
			fixture.AssertRawValue(t, pg, gssapiTypes[i], value)
			values[i] = fixture.Normalize(value)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, want, true); !ok {
		t.Fatalf("GSSAPI row bag=%#v want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func gssapiRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, prepared bool, query string, want [][]any, args ...any) {
	t.Helper()
	var statement *sql.Stmt
	var rows *sql.Rows
	var err error
	if prepared {
		statement, err = x.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		rows, err = statement.QueryContext(p.Ctx, args...)
	} else {
		rows, err = x.QueryContext(p.Ctx, query, args...)
	}
	if err != nil {
		t.Fatal(err)
	}
	gssapiConsume(t, rows, b.PG, want)
}

func gssapiBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, query string, want [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		gssapiRead(t, p, b, x, prepared, query, want, args...)
	}
}

func gssapiExec(t *testing.T, p *fixture.Pair, x fixture.Executor, query string) {
	t.Helper()
	result, err := x.ExecContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 0 {
		t.Fatalf("%s affected=%d error=%v", query, count, err)
	}
}

func gssapiConn(t *testing.T, p *fixture.Pair, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
	return conn
}

func gssapiPID(t *testing.T, p *fixture.Pair, x fixture.Executor) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT pg_backend_pid()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pid int64
	if !rows.Next() {
		t.Fatal("missing backend PID", rows.Err())
	}
	if err := rows.Scan(&pid); err != nil || pid <= 0 || pid > 2147483647 {
		t.Fatalf("positive int4 PID=%d error=%v", pid, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("backend PID cardinality", rows.Err())
	}
	return pid
}

func gssapiLogin(t *testing.T, p *fixture.Pair, b fixture.Backend, role string, idle int) (*sql.DB, *sql.Conn) {
	t.Helper()
	driver := p.NativeConnector.Driver().(*stuntgres.Driver)
	pool := fixture.RoleScenarioLoginPool(t, driver, b.PG, p.NativeDSN, role)
	pool.SetMaxIdleConns(idle)
	return pool, gssapiConn(t, p, pool)
}

func gssapiCreateRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, roles ...string) {
	t.Helper()
	for _, role := range roles {
		gssapiExec(t, p, b.DB, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'")
	}
}

func gssapiClear(t *testing.T, p *fixture.Pair, x fixture.Executor) {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT pg_catalog.pg_stat_clear_snapshot()")
	if err != nil {
		t.Fatal(err)
	}
	fixture.ArrayReferenceConsumeHeld(t, rows, [][]any{{""}}, []string{"VOID"})
}

// PostgreSQL backend shutdown may complete after the socket has closed. Observe
// actual retirement through fresh authorized captures, bounded by fixture policy.
func gssapiRetire(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, pool *sql.DB, conn *sql.Conn, pid int64) {
	t.Helper()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			t.Fatal("backend did not retire", pid, err)
		}
		gssapiClear(t, p, observer)
		var count int64
		if err := observer.QueryRowContext(ctx, "SELECT count(*) FROM pg_catalog.pg_stat_gssapi WHERE pid=$1", pid).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return
		}
		if count != 1 {
			t.Fatal("duplicate backend identity", count)
		}
		runtime.Gosched()
	}
}

func gssapiSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT n FROM gssapi_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
}

func gssapiSetupSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	gssapiExec(t, p, b.DB, "CREATE TABLE gssapi_sentinel(n integer)")
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO gssapi_sentinel VALUES(42)", 1))
}
