package reference_test

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
func referenceDateRaw(t *testing.T, pg bool, value any) {
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

func referenceDateConsume(t *testing.T, rows *sql.Rows, pg bool, s referenceStep) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.columns) {
		t.Fatalf("columns %v want %v: %v", columns, s.columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.columns, s.types)
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
			if s.types[i] == "DATE" {
				referenceDateRaw(t, pg, value)
			} else {
				referenceAssertRawValue(t, pg, s.types[i], value)
			}
			row[i] = referenceNormalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.columns, s.types)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	referenceDateMetadata(t, pg, meta, s.columns, s.types)
	compared, matched := referenceCompareRows(values, s.rows, s.bag)
	if !matched {
		t.Fatalf("DATE rows bag=%v got %#v want %#v", s.bag, values, s.rows)
	}
	return compared
}

func referenceDateRecorded(t *testing.T, recorder *referenceRecorder, pg bool, s referenceStep) {
	t.Helper()
	recorder.mu.Lock()
	var recorded *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == s.query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil {
		recorder.mu.Unlock()
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
	recorder.mu.Unlock()
	for _, row := range captured {
		for i, value := range row {
			if s.types[i] == "DATE" {
				referenceDateRaw(t, pg, value)
			} else {
				referenceAssertRawValue(t, pg, s.types[i], value)
			}
			row[i] = referenceNormalize(value)
		}
	}
	if !slices.Equal(columns, s.columns) || !referenceRecordedRowsMatch(captured, s) {
		t.Fatalf("recorded DATE columns=%v rows=%#v", columns, captured)
	}
	if !closed || closeErr != nil {
		t.Fatalf("recorded DATE closure %v/%v", closed, closeErr)
	}
}

func referenceDateRunStep(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	if !s.selectRows || s.state != "" || !slices.Contains(s.types, "DATE") {
		return referenceRunStep(t, ctx, db, recorder, s)
	}
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	var stmt *sql.Stmt
	if s.prepared {
		var err error
		stmt, err = db.PrepareContext(ctx, s.query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		db = referenceHeldStatement{referenceExecutor: db, statement: stmt, query: s.query}
	}
	rows, err := db.QueryContext(ctx, s.query, s.args...)
	if err != nil {
		t.Fatal(err)
	}
	pg := recorder.rawBackend == referenceRawPostgres
	values := referenceDateConsume(t, rows, pg, s)
	referenceDateRecorded(t, recorder, pg, s)
	return referenceObserved{columns: s.columns, types: s.types, rows: values}
}

// Canonical expected DATE text is authored in the corpus, never read from SQL.
func referenceDateExpected(pg bool, canonical string) any {
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
