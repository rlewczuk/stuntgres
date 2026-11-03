package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const archiverSQL = "SELECT * FROM pg_catalog.pg_stat_archiver"

var archiverNames = []string{"archived_count", "last_archived_wal", "last_archived_time", "failed_count", "last_failed_wal", "last_failed_time", "stats_reset"}
var archiverTypes = []string{"INT8", "TEXT", "TIMESTAMPTZ", "INT8", "TEXT", "TIMESTAMPTZ", "TIMESTAMPTZ"}

type archiverRow struct {
	archived, failed     int64
	success, failure     sql.NullString
	successAt, failureAt sql.NullTime
	reset                time.Time
}

func archiverMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, archiverNames) {
		t.Fatal("seven PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal("seven descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[time.Time]()
		if i == 0 || i == 3 {
			scan = reflect.TypeFor[int64]()
		} else if i == 1 || i == 4 {
			scan = reflect.TypeFor[string]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != archiverTypes[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), names[i], archiverTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable %t/%t", i, nullable, known)
		}
		length, lengthKnown := int64(0), false
		if i == 1 || i == 4 {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, known := typ.Length(); n != length || known != lengthKnown {
			t.Fatalf("column %d length %d/%t want %d/%t", i, n, known, length, lengthKnown)
		}
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("column %d precision %d/%d/%t", i, p, s, known)
		}
	}
}

func archiverRead(t *testing.T, rows *sql.Rows, pg bool) archiverRow {
	t.Helper()
	defer rows.Close()
	archiverMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("missing singleton", rows.Err())
	}
	var row archiverRow
	if err := rows.Scan(&row.archived, &row.success, &row.successAt, &row.failed, &row.failure, &row.failureAt, &row.reset); err != nil {
		t.Fatal("initialized archiver row", err)
	}
	if row.archived != 0 || row.failed != 0 || row.success.Valid || row.failure.Valid || row.successAt.Valid || row.failureAt.Valid {
		t.Fatal("absent physical archiver must not invent activity", row)
	}
	if row.reset.IsZero() || row.reset.Nanosecond()%1000 != 0 {
		t.Fatal("genuine microsecond reset metadata", row.reset)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("exactly one row", rows.Err())
	}
	return row
}

func archiverGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) archiverRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, archiverSQL)
	if err != nil {
		t.Fatal(err)
	}
	return archiverRead(t, rows, b.PG)
}

func archiverHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func archiverSame(t *testing.T, before, after archiverRow) {
	t.Helper()
	if before.archived != after.archived || before.failed != after.failed || before.success != after.success || before.failure != after.failure || before.successAt != after.successAt || before.failureAt != after.failureAt || !before.reset.Equal(after.reset) {
		t.Fatalf("unexpected archiver change: %#v -> %#v", before, after)
	}
}

func archiverClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var stamp time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func archiverEvent(t *testing.T, before, after archiverRow, lower, upper time.Time) {
	t.Helper()
	if after.reset.Before(before.reset) || after.reset.Before(lower) || after.reset.After(upper) {
		t.Fatalf("reset outside event bounds [%v,%v]: %v -> %v", lower, upper, before.reset, after.reset)
	}
}

func archiverVoid(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, name string, args ...any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(query, []string{name}, []string{"VOID"}, [][]any{{""}}, args...))
}

func archiverDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func archiverSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM archiver_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
