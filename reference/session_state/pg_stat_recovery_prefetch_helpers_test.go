package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const prefetchSQL = "SELECT * FROM pg_catalog.pg_stat_recovery_prefetch"

var prefetchNames = []string{"stats_reset", "prefetch", "hit", "skip_init", "skip_new", "skip_fpw", "skip_rep", "wal_distance", "block_distance", "io_depth"}
var prefetchTypes = []string{"TIMESTAMPTZ", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT4", "INT4", "INT4"}

type prefetchRow struct {
	reset time.Time
	technical [9]int64
}

func prefetchMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, prefetchNames) {
		t.Fatal("ten PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 10 {
		t.Fatal("ten descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 0 {
			scan = reflect.TypeFor[time.Time]()
		} else if pg && i >= 7 {
			scan = reflect.TypeFor[int32]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != prefetchTypes[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), names[i], prefetchTypes[i], scan)
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable %t/%t", i, nullable, known)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatalf("column %d length %d/%t", i, n, known)
		}
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("column %d precision %d/%d/%t", i, p, s, known)
		}
	}
}

func prefetchRead(t *testing.T, rows *sql.Rows, pg bool) prefetchRow {
	t.Helper()
	defer rows.Close()
	prefetchMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("missing singleton", rows.Err())
	}
	var row prefetchRow
	targets := []any{&row.reset}
	for i := range row.technical {
		targets = append(targets, &row.technical[i])
	}
	if err := rows.Scan(targets...); err != nil {
		t.Fatal("all ten initialized cells must be non-NULL", err)
	}
	if row.reset.IsZero() || row.reset.Nanosecond()%1000 != 0 {
		t.Fatal("genuine microsecond reset metadata", row.reset)
	}
	if row.technical != [9]int64{} {
		t.Fatal("absent recovery facility must not invent physical activity", row.technical)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("exactly one row", rows.Err())
	}
	return row
}

func prefetchGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) prefetchRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, prefetchSQL)
	if err != nil {
		t.Fatal(err)
	}
	return prefetchRead(t, rows, b.PG)
}

func prefetchHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func prefetchSame(t *testing.T, before, after prefetchRow) {
	t.Helper()
	if !before.reset.Equal(after.reset) || before.technical != after.technical {
		t.Fatalf("unexpected prefetch change: %#v -> %#v", before, after)
	}
}

func prefetchClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var stamp time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func prefetchEvent(t *testing.T, before, after prefetchRow, lower, upper time.Time) {
	t.Helper()
	if after.reset.Before(before.reset) || after.reset.Before(lower) || after.reset.After(upper) {
		t.Fatalf("reset outside same-backend event bounds [%v,%v]: %v -> %v", lower, upper, before.reset, after.reset)
	}
}

func prefetchSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM prefetch_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
