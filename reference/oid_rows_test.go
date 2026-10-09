package reference_test

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// pq exports OID as owned decimal bytes; the native adapter exports int64.
// Validate that family-local contract before the fixture's ordinary normalization.
func oidReferenceValue(t *testing.T, pg bool, typ string, value any) any {
	t.Helper()
	if typ != "OID" {
		referenceAssertRawValue(t, pg, typ, value)
		return referenceNormalize(value)
	}
	if value == nil {
		return nil
	}
	if !pg {
		n, ok := value.(int64)
		if !ok || n < 0 || n > 4294967295 {
			t.Fatalf("native OID %T %#v", value, value)
		}
		return n
	}
	encoded, ok := value.([]byte)
	if !ok || encoded == nil {
		t.Fatalf("pq OID %T %#v", value, value)
	}
	n, err := strconv.ParseUint(string(encoded), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	return int64(n)
}

func oidReferenceRows(t *testing.T, ctx context.Context, backend referenceBackend, executor referenceExecutor, step referenceStep) {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, backend.recorder)
	if step.prepared {
		stmt, err := executor.PrepareContext(ctx, step.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor = referenceHeldStatement{referenceExecutor: executor, statement: stmt, query: step.query}
	}
	rows, err := executor.QueryContext(ctx, step.query, step.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(columns, step.columns) || len(types) != len(step.types) {
		t.Fatal(columns, step.columns, types)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != step.types[i] {
			t.Fatal(typ, step.types[i])
		}
		if step.types[i] == "OID" {
			scan := reflect.TypeFor[int64]()
			if backend.pg {
				scan = reflect.TypeFor[any]()
			}
			if typ.ScanType() != scan {
				t.Fatal(typ)
			}
			if _, ok := typ.Length(); ok {
				t.Fatal("OID length")
			}
			if _, _, ok := typ.DecimalSize(); ok {
				t.Fatal("OID precision")
			}
		}
	}
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range dest {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = oidReferenceValue(t, backend.pg, step.types[i], row[i])
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, matches := referenceCompareRows(values, step.rows, step.bag); !matches {
		t.Fatal(step.query, values, step.rows)
	}
	backend.recorder.mu.Lock()
	var recorded *referenceOperation
	for _, op := range backend.recorder.operations {
		if op.SQL == step.query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	backend.recorder.mu.Unlock()
	if recorded == nil || !slices.Equal(recorded.Columns, step.columns) || !recorded.Closed || recorded.CloseErr != nil {
		t.Fatal("missing consumed/closed OID recording")
	}
	captured := make([][]any, len(recorded.Rows))
	for i, row := range recorded.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = oidReferenceValue(t, backend.pg, step.types[j], value)
		}
	}
	if _, matches := referenceCompareRows(captured, step.rows, step.bag); !matches {
		t.Fatal("recorded OID rows", captured, step.rows)
	}
}
