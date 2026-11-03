package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const bgwriterSQL = "SELECT * FROM pg_catalog.pg_stat_bgwriter"

var bgwriterNames = []string{"buffers_clean", "maxwritten_clean", "buffers_alloc", "stats_reset"}
var bgwriterTypes = []string{"INT8", "INT8", "INT8", "TIMESTAMPTZ"}

type bgwriterRow struct {
	clean, limited, allocated int64
	reset time.Time
}

func bgwriterMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, bgwriterNames) {
		t.Fatal("four PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal("four descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 3 {
			scan = reflect.TypeFor[time.Time]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != bgwriterTypes[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable %t/%t", i, nullable, known)
		}
		if length, known := typ.Length(); length != 0 || known {
			t.Fatalf("column %d length %d/%t", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("column %d precision %d/%d/%t", i, precision, scale, known)
		}
	}
}

func bgwriterRead(t *testing.T, rows *sql.Rows, pg bool) bgwriterRow {
	t.Helper()
	defer rows.Close()
	bgwriterMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("missing singleton", rows.Err())
	}
	var row bgwriterRow
	if err := rows.Scan(&row.clean, &row.limited, &row.allocated, &row.reset); err != nil {
		t.Fatal("initialized bgwriter row", err)
	}
	if row.clean < 0 || row.limited < 0 || row.allocated < 0 {
		t.Fatal("negative physical counters", row)
	}
	if !pg && (row.clean != 0 || row.limited != 0 || row.allocated != 0) {
		t.Fatal("absent physical facilities must not invent activity", row)
	}
	if row.reset.IsZero() || row.reset.Nanosecond()%1000 != 0 {
		t.Fatal("genuine microsecond reset metadata", row.reset)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("exactly one row", rows.Err())
	}
	return row
}

func bgwriterGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) bgwriterRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, bgwriterSQL)
	if err != nil {
		t.Fatal(err)
	}
	return bgwriterRead(t, rows, b.PG)
}

func bgwriterHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	return archiverHeld(t, p, c, query)
}

// Physical PostgreSQL counters can drift independently of logical reset metadata.
func bgwriterSameReset(t *testing.T, before, after bgwriterRow) {
	t.Helper()
	if !before.reset.Equal(after.reset) {
		t.Fatal("unexpected reset change", before, after)
	}
}

func bgwriterSameFact(t *testing.T, before, after bgwriterRow) {
	t.Helper()
	bgwriterSameReset(t, before, after)
	if before.clean != after.clean || before.limited != after.limited || before.allocated != after.allocated {
		t.Fatal("retained counters changed", before, after)
	}
}

func bgwriterClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	return archiverClock(t, p, c)
}

func bgwriterEvent(t *testing.T, before, after bgwriterRow, lower, upper time.Time) {
	t.Helper()
	if after.reset.Before(before.reset) || after.reset.Before(lower) || after.reset.After(upper) {
		t.Fatalf("reset outside event bounds [%v,%v]: %v -> %v", lower, upper, before.reset, after.reset)
	}
}

func bgwriterVoid(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, name string, args ...any) {
	t.Helper()
	archiverVoid(t, p, b, e, query, name, args...)
}

func bgwriterDiagnostic(code, message string) fixture.Diagnostic {
	return archiverDiagnostic(code, message)
}

func bgwriterSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM bgwriter_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
