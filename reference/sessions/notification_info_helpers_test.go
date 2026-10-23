package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const notificationInfoUsageSQL = "SELECT pg_notification_queue_usage()"

// Inquiry scenarios need recorded SQL results, not a raw notification receiver.
func notificationInfoConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		_, _ = conn.ExecContext(ctx, "UNLISTEN *")
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn
}

func notificationInfoHeld(t *testing.T, p *fixture.Pair, conn *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("prepare notification inquiry:", err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
}

func notificationInfoChannels(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query string, names ...string) {
	t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	label := "pg_listening_channels"
	if strings.Contains(query, "AS c(channel)") {
		label = "channel"
	}
	s := fixture.Select(query, []string{label}, []string{"TEXT"}, want)
	s.Bag = !strings.Contains(query, "ORDER BY")
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != 1 || columns[0].ScanType() != reflect.TypeOf("") {
			t.Fatal("listening channel must have string ScanType", columns)
		}
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var channel string
		if err := rows.Scan(&channel); err != nil {
			t.Fatal("non-NULL TEXT channel:", err)
		}
	}
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
}

func notificationInfoUsage(t *testing.T, p *fixture.Pair, executor fixture.Executor) float64 {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, notificationInfoUsageSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "pg_notification_queue_usage" {
		t.Fatal("queue column names:", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0].DatabaseTypeName() != "FLOAT8" || types[0].ScanType() != reflect.TypeOf(float64(0)) {
		t.Fatal("queue must expose FLOAT8/float64 metadata", types)
	}
	if !rows.Next() {
		t.Fatal("queue inquiry must return one non-NULL row", rows.Err())
	}
	var usage float64
	if err := rows.Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("queue inquiry must return exactly one row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(usage) || math.IsInf(usage, 0) || usage < 0 || usage > 1 {
		t.Fatalf("queue fraction outside finite [0,1]: %g", usage)
	}
	return usage
}

func notificationInfoWantUsage(t *testing.T, p *fixture.Pair, executor fixture.Executor, want float64) {
	t.Helper()
	if got := notificationInfoUsage(t, p, executor); got != want {
		t.Fatalf("queue fraction = %g, want %g", got, want)
	}
}

// Eight distinct entries cross pages even when the shared oracle head starts
// partway through a page. Do not pin their fraction to a fresh head position.
func notificationInfoBatch(t *testing.T, p *fixture.Pair, b fixture.Backend, sender fixture.Executor, channel, ending string) {
	t.Helper()
	sessionMessageSQL(t, p, b, sender, "BEGIN")
	for i := range 8 {
		payload := strings.Repeat("x", 7890) + fmt.Sprint(i)
		sessionMessageSQL(t, p, b, sender, "NOTIFY "+channel+", '"+payload+"'")
	}
	sessionMessageSQL(t, p, b, sender, ending)
}
