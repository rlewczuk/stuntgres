package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// randomRead consumes nondeterministic results once. Expectations for their
// ranges and same-session dependencies belong to the calling scenario.
func randomRead(t *testing.T, ctx context.Context, e fixture.Executor, b fixture.Backend, q string, names, types []string, checkScan func(t *testing.T, rows *sql.Rows, rowIndex int), args ...any) [][]any {
	t.Helper()
	ctx = context.WithValue(ctx, fixture.RecorderKey{}, b.Recorder)
	rows, err := e.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, m := range meta {
		if m.DatabaseTypeName() != types[i] {
			t.Fatal(m, types[i])
		}
	}
	values := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(types))
		dest := make([]any, len(types))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			fixture.AssertRawValue(t, b.PG, types[i], v)
		}
		if checkScan != nil {
			checkScan(t, rows, len(values))
		}
		for i, v := range row {
			row[i] = fixture.Normalize(v)
		}
		values = append(values, row)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	randomRecorded(t, b, q, names, types, values)
	return values
}
func randomRecorded(t *testing.T, b fixture.Backend, q string, names, types []string, values [][]any) {
	t.Helper()
	b.Recorder.Mu.Lock()
	defer b.Recorder.Mu.Unlock()
	var captured *fixture.Operation
	for _, op := range b.Recorder.Operations {
		if op.SQL == q && op.Err == nil && op.Columns != nil {
			captured = op
		}
	}
	if captured == nil || !captured.Closed || captured.CloseErr != nil || !reflect.DeepEqual(captured.Columns, names) {
		t.Fatal("consumed random recording/closure", q)
	}
	normalized := make([][]any, 0, len(captured.Rows))
	for _, raw := range captured.Rows {
		row := make([]any, len(raw))
		for i, v := range raw {
			fixture.AssertRawValue(t, b.PG, types[i], v)
			row[i] = randomRecordedCell(fixture.Normalize(v))
		}
		normalized = append(normalized, row)
	}
	expected := make([][]any, len(values))
	for i, row := range values {
		expected[i] = make([]any, len(row))
		for j, v := range row {
			expected[i][j] = randomRecordedCell(v)
		}
	}
	if !fixture.RecordedRowsMatch(normalized, fixture.Select(q, names, types, expected)) {
		t.Fatal("recorded random values", q)
	}
}

// NaN has no equality relation even to itself; retain its explicit class only
// for the recorder comparison. The public result remains the raw float64.
func randomRecordedCell(v any) any {
	if f, ok := v.(float64); ok && math.IsNaN(f) {
		return "NaN"
	}
	return v
}
func randomScalar(t *testing.T, p *fixture.Pair, c fixture.Executor, b fixture.Backend, q, name, typ string, args ...any) any {
	t.Helper()
	rows := randomRead(t, p.Ctx, c, b, q, []string{name}, []string{typ}, nil, args...)
	if len(rows) != 1 {
		t.Fatal("scalar row count", rows)
	}
	return rows[0][0]
}
func randomReseed(t *testing.T, p *fixture.Pair, c fixture.Executor, b fixture.Backend, seed float64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT setseed($1)", []string{"setseed"}, []string{"VOID"}, [][]any{{""}}, seed))
}
