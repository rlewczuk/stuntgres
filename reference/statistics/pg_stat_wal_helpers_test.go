package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const walSQL = "SELECT * FROM pg_catalog.pg_stat_wal"

var walNames = []string{"wal_records", "wal_fpi", "wal_bytes", "wal_buffers_full", "stats_reset"}
var walTypes = []string{"INT8", "INT8", "NUMERIC", "INT8", "TIMESTAMPTZ"}

type walRow struct {
	records, fpi, full int64
	bytes              string
	reset              time.Time
	physical           bool
}

func walMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, walNames) {
		t.Fatal("five PG18 WAL columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 5 {
		t.Fatal("five descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		} else if i == 4 {
			scan = reflect.TypeFor[time.Time]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != walTypes[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), names[i], walTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable %t/%t", i, nullable, known)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatalf("column %d length %d/%t", i, n, known)
		}
		precision, scale, known := int64(0), int64(0), false
		if pg && i == 2 {
			precision, scale, known = 65535, 65531, true
		}
		if p, s, k := typ.DecimalSize(); p != precision || s != scale || k != known {
			t.Fatalf("column %d precision %d/%d/%t want %d/%d/%t", i, p, s, k, precision, scale, known)
		}
	}
}

func walRead(t *testing.T, rows *sql.Rows, pg bool) walRow {
	t.Helper()
	defer rows.Close()
	walMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("missing WAL singleton", rows.Err())
	}
	var row walRow
	row.physical = pg
	if err := rows.Scan(&row.records, &row.fpi, &row.bytes, &row.full, &row.reset); err != nil {
		t.Fatal("initialized WAL row", err)
	}
	if row.records < 0 || row.fpi < 0 || row.full < 0 || row.bytes == "" || strings.Trim(row.bytes, "0123456789") != "" {
		t.Fatal("nonnegative integral physical counters", row)
	}
	if !pg && (row.records != 0 || row.fpi != 0 || row.bytes != "0" || row.full != 0) {
		t.Fatal("absent physical WAL must produce exactly typed zero estimates", row)
	}
	if row.reset.IsZero() || row.reset.Nanosecond()%1000 != 0 {
		t.Fatal("genuine microsecond WAL reset metadata", row.reset)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("exactly one WAL row", rows.Err())
	}
	return row
}

func walGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) walRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, walSQL)
	if err != nil {
		t.Fatal(err)
	}
	return walRead(t, rows, b.PG)
}

func walSame(t *testing.T, before, after walRow) {
	t.Helper()
	// A real PostgreSQL cluster may publish physical WAL between commands.
	// Reset authority, unlike physical counter totals, has an exact stable cut.
	sameCounters := before.records == after.records && before.fpi == after.fpi &&
		before.bytes == after.bytes && before.full == after.full
	if !before.reset.Equal(after.reset) || (!before.physical && !after.physical && !sameCounters) {
		t.Fatalf("unexpected WAL authority change: %#v -> %#v", before, after)
	}
}

func walEvent(t *testing.T, before, after walRow, lower, upper time.Time) {
	t.Helper()
	if after.reset.Before(before.reset) || after.reset.Before(lower) || after.reset.After(upper) {
		t.Fatalf("reset outside event bounds [%v,%v]: %v -> %v", lower, upper, before.reset, after.reset)
	}
}

func walSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM wal_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
