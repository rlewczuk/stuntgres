package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// DATE infinity is a byte payload in pq despite its time.Time descriptor.
// Keep this domain check before normalization rather than changing the fixture.
func DateRaw(t *testing.T, pg bool, value any) {
	t.Helper()
	if value == nil {
		return
	}
	if !pg {
		if _, ok := value.(string); !ok {
			t.Fatalf("native DATE %T %#v", value, value)
		}
		return
	}
	if date, ok := value.(time.Time); ok {
		_, offset := date.Zone()
		if offset != 0 || date.Hour() != 0 || date.Minute() != 0 || date.Second() != 0 || date.Nanosecond() != 0 {
			t.Fatalf("raw DATE %v", date)
		}
		return
	}
	bytes, ok := value.([]byte)
	if !ok || bytes == nil || (string(bytes) != "infinity" && string(bytes) != "-infinity") {
		t.Fatalf("postgres DATE %T %#v", value, value)
	}
}

func referenceDateMetadata(t *testing.T, pg bool, meta []*sql.ColumnType, columns, types []string) {
	t.Helper()
	if len(meta) != len(types) {
		t.Fatalf("metadata %#v want %v", meta, types)
	}
	for i, typ := range meta {
		if typ.Name() != columns[i] || typ.DatabaseTypeName() != types[i] {
			t.Fatalf("metadata %s/%s want %s/%s", typ.Name(), typ.DatabaseTypeName(), columns[i], types[i])
		}
		if types[i] != "DATE" {
			continue
		}
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[time.Time]()
		}
		if typ.ScanType() != scan {
			t.Fatalf("DATE ScanType %v want %v", typ.ScanType(), scan)
		}
		if _, _, known := typ.DecimalSize(); known {
			t.Fatal("DATE decimal precision must be unknown")
		}
		if _, known := typ.Length(); known {
			t.Fatal("DATE length must be unknown")
		}
		if _, known := typ.Nullable(); pg && known {
			t.Fatal("pq DATE nullability must be unknown")
		}
	}
}

func DateConsume(t *testing.T, rows *sql.Rows, pg bool, s Step) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.Columns) {
		t.Fatalf("columns %v want %v: %v", columns, s.Columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.Columns, s.Types)
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if s.Types[i] == "DATE" {
				DateRaw(t, pg, value)
			} else {
				AssertRawValue(t, pg, s.Types[i], value)
			}
			row[i] = Normalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.Columns, s.Types)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.Columns, s.Types)
	compared, matched := CompareRows(values, s.Rows, s.Bag)
	if !matched {
		t.Fatalf("DATE rows bag=%v got %#v want %#v", s.Bag, values, s.Rows)
	}
	return compared
}

func DateRecorded(t *testing.T, recorder *Recorder, pg bool, s Step) {
	t.Helper()
	recorder.Mu.Lock()
	var recorded *Operation
	for _, op := range recorder.Operations {
		if op.SQL == s.Query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil {
		recorder.Mu.Unlock()
		t.Fatal("DATE query has no recorded result")
	}
	columns := slices.Clone(recorded.Columns)
	captured := make([][]any, len(recorded.Rows))
	for i, row := range recorded.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = referenceCopyValue(value)
		}
	}
	closed, closeErr := recorded.Closed, recorded.CloseErr
	recorder.Mu.Unlock()
	for _, row := range captured {
		for i, value := range row {
			if s.Types[i] == "DATE" {
				DateRaw(t, pg, value)
			} else {
				AssertRawValue(t, pg, s.Types[i], value)
			}
			row[i] = Normalize(value)
		}
	}
	if !slices.Equal(columns, s.Columns) || !RecordedRowsMatch(captured, s) {
		t.Fatalf("recorded DATE columns=%v rows=%#v", columns, captured)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded DATE closure %v/%v", closed, closeErr)
	}
}

func DateRunStep(t *testing.T, ctx context.Context, db Executor, recorder *Recorder, s Step) Observed {
	t.Helper()
	if !s.SelectRows || s.State != "" || !slices.Contains(s.Types, "DATE") {
		return RunStep(t, ctx, db, recorder, s)
	}
	ctx = context.WithValue(ctx, RecorderKey{}, recorder)
	var stmt *sql.Stmt
	if s.Prepared {
		var err error
		stmt, err = db.PrepareContext(ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		db = HeldStatement{Executor: db, Statement: stmt, SQL: s.Query}
	}
	rows, err := db.QueryContext(ctx, s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	pg := recorder.RawBackend == RawPostgres
	values := DateConsume(t, rows, pg, s)
	DateRecorded(t, recorder, pg, s)
	return Observed{Columns: s.Columns, types: s.Types, Rows: values}
}

// Canonical expected DATE text is authored in the corpus, never read from SQL.
func DateExpected(pg bool, canonical string) any {
	if !pg || canonical == "infinity" || canonical == "-infinity" {
		return canonical
	}
	bc := strings.HasSuffix(canonical, " BC")
	parts := strings.Split(strings.TrimSuffix(canonical, " BC"), "-")
	if len(parts) != 3 {
		panic("invalid canonical DATE expectation: " + canonical)
	}
	year, yerr := strconv.Atoi(parts[0])
	month, merr := strconv.Atoi(parts[1])
	day, derr := strconv.Atoi(parts[2])
	if yerr != nil || merr != nil || derr != nil {
		panic("invalid canonical DATE expectation: " + canonical)
	}
	if bc {
		year = 1 - year
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
