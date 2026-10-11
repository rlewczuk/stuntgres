package fixture

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// pq exports OID as owned decimal bytes; the native adapter exports int64.
// Validate that family-local contract before the fixture's ordinary normalization.
func OidReferenceValue(t *testing.T, pg bool, typ string, value any) any {
	t.Helper()
	if typ != "OID" {
		AssertRawValue(t, pg, typ, value)
		return Normalize(value)
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

func OidReferenceRows(t *testing.T, ctx context.Context, backend Backend, executor Executor, step Step) {
	t.Helper()
	ctx = context.WithValue(ctx, RecorderKey{}, backend.Recorder)
	if step.Prepared {
		stmt, err := executor.PrepareContext(ctx, step.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor = HeldStatement{Executor: executor, Statement: stmt, SQL: step.Query}
	}
	rows, err := executor.QueryContext(ctx, step.Query, step.Args...)
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
	if !slices.Equal(columns, step.Columns) || len(types) != len(step.Types) {
		t.Fatal(columns, step.Columns, types)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != step.Types[i] {
			t.Fatal(typ, step.Types[i])
		}
		if step.Types[i] == "OID" {
			scan := reflect.TypeFor[int64]()
			if backend.PG {
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
			row[i] = OidReferenceValue(t, backend.PG, step.Types[i], row[i])
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, matches := CompareRows(values, step.Rows, step.Bag); !matches {
		t.Fatal(step.Query, values, step.Rows)
	}
	backend.Recorder.Mu.Lock()
	var recorded *Operation
	for _, op := range backend.Recorder.Operations {
		if op.SQL == step.Query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	backend.Recorder.Mu.Unlock()
	if recorded == nil || !slices.Equal(recorded.Columns, step.Columns) || !recorded.Closed || recorded.CloseErr != nil {
		t.Fatal("missing consumed/closed OID recording")
	}
	captured := make([][]any, len(recorded.Rows))
	for i, row := range recorded.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = OidReferenceValue(t, backend.PG, step.Types[j], value)
		}
	}
	if _, matches := CompareRows(captured, step.Rows, step.Bag); !matches {
		t.Fatal("recorded OID rows", captured, step.Rows)
	}
}
