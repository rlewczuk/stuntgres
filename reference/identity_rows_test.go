package reference_test

import (
	"context"
	"reflect"
	"testing"
)

// An unordered source does not couple a payload to a particular generated ID.
// Check both independent bags per insertion phase, plus the complete owned raw
// recording, without changing the original SQL or accepting lost duplicates.
func identityUncoupledRows(t *testing.T, ctx context.Context, executor referenceExecutor, recorder *referenceRecorder, q string, want [][]any, phaseSize int) {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	rows, err := executor.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"id", "p"}) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" {
			t.Fatal(typ.DatabaseTypeName())
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		var id, payload any
		if err := rows.Scan(&id, &payload); err != nil {
			t.Fatal(err)
		}
		referenceAssertRawValue(t, true, "INT4", id)
		referenceAssertRawValue(t, true, "INT4", payload)
		got = append(got, []any{id, payload})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal("row count", len(got), len(want))
	}
	for start := 0; start < len(want); start += phaseSize {
		actual, expected := make([][]any, phaseSize), make([][]any, phaseSize)
		for i := range phaseSize {
			if got[start+i][0] != want[start+i][0] {
				t.Fatal("generated ID", got[start+i], want[start+i])
			}
			actual[i], expected[i] = []any{got[start+i][1]}, []any{want[start+i][1]}
		}
		if _, ok := referenceCompareRows(actual, expected, true); !ok {
			t.Fatal("phase payload bag", actual, expected)
		}
	}
	recorder.mu.Lock()
	var operation *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == q && op.Err == nil && op.Columns != nil {
			operation = op
		}
	}
	if operation == nil {
		recorder.mu.Unlock()
		t.Fatal("missing owned recording")
	}
	recordedRows, columns, closed, closeErr := operation.Rows, operation.Columns, operation.Closed, operation.CloseErr
	recorder.mu.Unlock()
	captured := make([][]any, len(recordedRows))
	for i, row := range recordedRows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			referenceAssertRawValue(t, true, "INT4", value)
			captured[i][j] = value
		}
	}
	if !reflect.DeepEqual(columns, cols) || !reflect.DeepEqual(captured, got) || !closed || closeErr != nil {
		t.Fatal("owned raw recording/closure", columns, captured, closed, closeErr)
	}
}
