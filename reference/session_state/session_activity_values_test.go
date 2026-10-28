package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const activitySQL = "SELECT current_query(), pg_backend_pid(), pg_postmaster_start_time(), pg_conf_load_time()"
var activityNames = []string{"current_query", "pg_backend_pid", "pg_postmaster_start_time", "pg_conf_load_time"}

type activitySnapshot struct { Query string; PID int64; Start, Load time.Time }

func activityScenario(t *testing.T, fn func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		c, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer c.Close()
		fn(t, p, b, c)
	}) }
}

func activityRead(t *testing.T, ctx context.Context, rows *sql.Rows, source string, pg bool) activitySnapshot {
	t.Helper(); defer rows.Close()
	names, err := rows.Columns(); if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(names, activityNames) { t.Fatalf("names %v", names) }
	columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
	for i, want := range []string{"TEXT", "INT4", "TIMESTAMPTZ", "TIMESTAMPTZ"} {
		if columns[i].DatabaseTypeName() != want { t.Fatalf("type %d: %s", i, columns[i].DatabaseTypeName()) }
		if nullable, known := columns[i].Nullable(); nullable || known { t.Fatalf("computed nullability %d: %v %v", i, nullable, known) }
		var typ reflect.Type
		switch i { case 0: typ = reflect.TypeFor[string](); case 1: typ = reflect.TypeFor[int64](); if pg { typ = reflect.TypeFor[int32]() }; default: typ = reflect.TypeFor[time.Time]() }
		if columns[i].ScanType() != typ { t.Fatalf("scan type %d: %v want %v", i, columns[i].ScanType(), typ) }
	}
	if !rows.Next() { t.Fatalf("missing row: %v", rows.Err()) }
	var raw [4]any
	if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil { t.Fatal(err) }
	q, ok := raw[0].(string); if !ok || q != source { t.Fatalf("source %#v want %q", raw[0], source) }
	pid, ok := raw[1].(int64); if !ok || pid <= 0 || pid > 2147483647 { t.Fatalf("pid %#v", raw[1]) }
	start, ok := raw[2].(time.Time); if !ok { t.Fatalf("start %#v", raw[2]) }
	load, ok := raw[3].(time.Time); if !ok { t.Fatalf("load %#v", raw[3]) }
	for _, stamp := range []time.Time{start, load} { if stamp.IsZero() || stamp.After(time.Now()) || stamp.Nanosecond()%1000 != 0 { t.Fatalf("event timestamp %v", stamp) } }
	if rows.Next() || rows.Err() != nil || rows.NextResultSet() { t.Fatalf("unexpected trailing result: %v", rows.Err()) }
	if err := ctx.Err(); err != nil { t.Fatal(err) }
	return activitySnapshot{q, pid, start, load}
}

func activityCapture(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) activitySnapshot {
	t.Helper(); rows, err := c.QueryContext(p.Ctx, activitySQL); if err != nil { t.Fatal(err) }
	return activityRead(t, p.Ctx, rows, activitySQL, b.PG)
}
func activitySameRuntime(t *testing.T, a, b activitySnapshot) { t.Helper(); if a.PID != b.PID || !a.Start.Equal(b.Start) || !a.Load.Equal(b.Load) { t.Fatalf("runtime changed: %#v -> %#v", a, b) } }
func activityExec(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) { t.Helper(); if _, err := c.ExecContext(p.Ctx, query); err != nil { t.Fatalf("%s: %v", query, err) } }

func TestReferenceSessionActivityValues(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := activityCapture(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, activitySQL); if err != nil { t.Fatal(err) }; defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx); if err != nil { t.Fatal(err) }
		activitySameRuntime(t, first, activityRead(t, p.Ctx, rows, activitySQL, b.PG))
		for _, exec := range []func() (sql.Result, error){func() (sql.Result,error) { return c.ExecContext(p.Ctx, activitySQL) }, func() (sql.Result,error) { return stmt.ExecContext(p.Ctx) }} {
			result, err := exec(); if err != nil { t.Fatal(err) }; n, err := result.RowsAffected(); if err != nil || n != 1 { t.Fatalf("affected %d %v", n, err) }
		}
		qualified := "SELECT pg_catalog.current_query() AS current_query, pg_catalog.pg_backend_pid() AS pg_backend_pid, pg_catalog.pg_postmaster_start_time() AS pg_postmaster_start_time, pg_catalog.pg_conf_load_time() AS pg_conf_load_time"
		rows, err = c.QueryContext(p.Ctx, qualified); if err != nil { t.Fatal(err) }
		activitySameRuntime(t, first, activityRead(t, p.Ctx, rows, qualified, b.PG))
		activityExec(t, p, c, "SET TIME ZONE 'America/New_York'")
		activitySameRuntime(t, first, activityCapture(t, p, b, c))
	})
}

func TestReferenceSessionActivityPreparedAndBatch(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"  /*λ*/ SELECT current_query(); SELECT current_query() AS q; -- tail\n", "/*"+strings.Repeat("λ", 2048)+"*/ SELECT current_query(); SELECT current_query() AS q"} {
			rows, err := c.QueryContext(p.Ctx, query); if err != nil { t.Fatal(err) }
			for i, name := range []string{"current_query", "q"} {
				if i > 0 && !rows.NextResultSet() { t.Fatalf("missing set: %v", rows.Err()) }
				names, err := rows.Columns(); if err != nil || !reflect.DeepEqual(names, []string{name}) { t.Fatalf("columns %v %v", names, err) }
				types, err := rows.ColumnTypes(); if err != nil || types[0].DatabaseTypeName() != "TEXT" { t.Fatalf("type %v %v", types, err) }
				var source string; if !rows.Next() { t.Fatal("missing source") }; if err := rows.Scan(&source); err != nil || source != query { t.Fatalf("source %q %v", source, err) }
				if rows.Next() || rows.Err() != nil { t.Fatalf("extra row %v", rows.Err()) }
			}
			if rows.NextResultSet() || rows.Err() != nil { t.Fatalf("extra set %v", rows.Err()) }; rows.Close()
			result, err := c.ExecContext(p.Ctx, query); if err != nil { t.Fatal(err) }; n, err := result.RowsAffected(); if err != nil || n != 1 { t.Fatalf("batch affected %d %v", n, err) }
		}
		query := "/*prepared λ*/ SELECT current_query(), $1::text"
		stmt, err := c.PrepareContext(p.Ctx, query); if err != nil { t.Fatal(err) }; defer stmt.Close()
		for _, arg := range []any{"first", nil, "second"} {
			activityExec(t, p, c, "SELECT 1")
			var source string; var value sql.NullString
			if err := stmt.QueryRowContext(p.Ctx, arg).Scan(&source, &value); err != nil { t.Fatal(err) }
			if source != query || value.Valid != (arg != nil) || arg != nil && value.String != arg { t.Fatalf("prepared %q %#v", source, value) }
			if _, err := stmt.ExecContext(p.Ctx, arg); err != nil { t.Fatal(err) }
			if err := c.QueryRowContext(p.Ctx, query, arg).Scan(&source, &value); err != nil || source != query { t.Fatalf("direct parameter %q %v", source, err) }
		}
		for _, query := range []string{"PREPARE activity_q AS SELECT current_query(); EXECUTE activity_q", "EXECUTE activity_q"} {
			var source string; if err := c.QueryRowContext(p.Ctx, query).Scan(&source); err != nil || source != query { t.Fatalf("SQL EXECUTE %q %v", source, err) }
		}
		activityExec(t, p, c, "CREATE FUNCTION activity_query() RETURNS text LANGUAGE SQL AS 'SELECT current_query()'")
		activityExec(t, p, c, "CREATE VIEW activity_query_view AS SELECT current_query() AS q")
		for _, query := range []string{"SELECT activity_query()", "SELECT q FROM activity_query_view"} { var source string; if err := c.QueryRowContext(p.Ctx, query).Scan(&source); err != nil || source != query { t.Fatalf("nested %q %v", source, err) } }
		activityExec(t, p, c, "BEGIN")
		declare := "DECLARE activity_c NO SCROLL CURSOR FOR SELECT current_query(), statement_timestamp()"
		var declared time.Time
		if err := c.QueryRowContext(p.Ctx, declare+"; SELECT statement_timestamp()").Scan(&declared); err != nil { t.Fatal(err) }
		activityExec(t,p,c,"SELECT 1")
		var source string; var fetchedAt, afterFetch time.Time
		fetch := "FETCH 1 FROM activity_c"
		if err := c.QueryRowContext(p.Ctx, fetch).Scan(&source, &fetchedAt); err != nil || source != fetch { t.Fatalf("cursor source %q want %q: %v", source, fetch, err) }
		if err := c.QueryRowContext(p.Ctx, "SELECT statement_timestamp()").Scan(&afterFetch); err != nil { t.Fatal(err) }
		if fetchedAt.IsZero() || fetchedAt.Nanosecond()%1000 != 0 || fetchedAt.Before(declared) || fetchedAt.After(afterFetch) { t.Fatalf("cursor timestamp %v outside backend command bounds [%v, %v]", fetchedAt, declared, afterFetch) }
		activityExec(t, p, c, "CLOSE activity_c"); activityExec(t, p, c, "COMMIT")
		bad, err := c.PrepareContext(p.Ctx, "SELECT current_query(); SELECT current_query()")
		if bad != nil { bad.Close(); t.Fatal("failed prepare published handle") }
		want := fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:"cannot insert multiple commands into a prepared statement"}
		fixture.AssertDiagnostic(t, err, &want)
	})
}
