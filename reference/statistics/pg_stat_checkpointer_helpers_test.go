package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const checkpointerSQL = "SELECT * FROM pg_catalog.pg_stat_checkpointer"

var checkpointerNames = []string{"num_timed", "num_requested", "num_done", "restartpoints_timed", "restartpoints_req", "restartpoints_done", "write_time", "sync_time", "buffers_written", "slru_written", "stats_reset"}
var checkpointerTypes = []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8", "FLOAT8", "INT8", "INT8", "TIMESTAMPTZ"}

type checkpointerRow struct {
	clean, limited, allocated                                                int64
	restartTimed, restartRequested, restartDone, buffersWritten, slruWritten int64
	writeTime, syncTime                                                      float64
	reset                                                                    time.Time
}

func checkpointerMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, checkpointerNames) {
		t.Fatal("eleven PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 11 {
		t.Fatal("eleven descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 6 || i == 7 {
			scan = reflect.TypeFor[float64]()
		}
		if i == 10 {
			scan = reflect.TypeFor[time.Time]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != checkpointerTypes[i] || typ.ScanType() != scan {
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

func checkpointerRead(t *testing.T, rows *sql.Rows, pg bool) checkpointerRow {
	t.Helper()
	defer rows.Close()
	checkpointerMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("missing singleton", rows.Err())
	}
	var row checkpointerRow
	if err := rows.Scan(&row.clean, &row.limited, &row.allocated, &row.restartTimed, &row.restartRequested, &row.restartDone, &row.writeTime, &row.syncTime, &row.buffersWritten, &row.slruWritten, &row.reset); err != nil {
		t.Fatal("initialized checkpointer row", err)
	}
	if row.clean < 0 || row.limited < 0 || row.allocated < 0 || row.restartTimed < 0 || row.restartRequested < 0 || row.restartDone < 0 || row.writeTime < 0 || row.syncTime < 0 || row.buffersWritten < 0 || row.slruWritten < 0 {
		t.Fatal("negative physical counters", row)
	}
	if !pg && (row.clean != 0 || row.limited != 0 || row.allocated != 0 || row.restartTimed != 0 || row.restartRequested != 0 || row.restartDone != 0 || row.writeTime != 0 || row.syncTime != 0 || row.buffersWritten != 0 || row.slruWritten != 0) {
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

func checkpointerGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) checkpointerRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, checkpointerSQL)
	if err != nil {
		t.Fatal(err)
	}
	return checkpointerRead(t, rows, b.PG)
}

func checkpointerHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	return archiverHeld(t, p, c, query)
}

// Physical PostgreSQL counters can drift independently of logical reset metadata.
func checkpointerSameReset(t *testing.T, before, after checkpointerRow) {
	t.Helper()
	if !before.reset.Equal(after.reset) {
		t.Fatal("unexpected reset change", before, after)
	}
}

func checkpointerSameFact(t *testing.T, before, after checkpointerRow) {
	t.Helper()
	checkpointerSameReset(t, before, after)
	if before.clean != after.clean || before.limited != after.limited || before.allocated != after.allocated || before.restartTimed != after.restartTimed || before.restartRequested != after.restartRequested || before.restartDone != after.restartDone || before.writeTime != after.writeTime || before.syncTime != after.syncTime || before.buffersWritten != after.buffersWritten || before.slruWritten != after.slruWritten {
		t.Fatal("retained counters changed", before, after)
	}
}

func checkpointerClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	return archiverClock(t, p, c)
}

func checkpointerEvent(t *testing.T, before, after checkpointerRow, lower, upper time.Time) {
	t.Helper()
	if after.reset.Before(before.reset) || after.reset.Before(lower) || after.reset.After(upper) {
		t.Fatalf("reset outside event bounds [%v,%v]: %v -> %v", lower, upper, before.reset, after.reset)
	}
}

func checkpointerVoid(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, name string, args ...any) {
	t.Helper()
	archiverVoid(t, p, b, e, query, name, args...)
}

func checkpointerDiagnostic(code, message string) fixture.Diagnostic {
	return archiverDiagnostic(code, message)
}

func checkpointerSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM checkpointer_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
