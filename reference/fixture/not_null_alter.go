package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func NotNullRetainedRows(t *testing.T, ctx context.Context, stmt *sql.Stmt, native, nullable bool, want [][]any) {
	t.Helper()
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		t.Fatal("retained SELECT", err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		wantType := "INT4"
		wantScan := reflect.TypeFor[int32]()
		wantNullable := false
		if native {
			wantScan = reflect.TypeFor[int64]()
			wantNullable = nullable
		}
		if i == 1 {
			wantType = "TEXT"
			wantScan = reflect.TypeFor[string]()
			wantNullable = native
		}
		gotNullable, known := typ.Nullable()
		// pq exposes INT4's int32 ScanType, but no Nullable capability. Native
		// exposes int64 and current known nullability; these are not parity fields.
		if typ.DatabaseTypeName() != wantType || typ.ScanType() != wantScan || known != native || gotNullable != wantNullable {
			t.Fatal(native, i, typ.DatabaseTypeName(), typ.ScanType(), gotNullable, known)
		}
	}
	var got [][]any
	for rows.Next() {
		var a, b any
		if err = rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		AssertRawValue(t, !native, "INT4", a)
		AssertRawValue(t, !native, "TEXT", b)
		got = append(got, []any{Normalize(a), Normalize(b)})
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal("retained cursor close", err)
	}
	record, ok := ctx.Value(RecorderKey{}).(*Recorder)
	if !ok {
		t.Fatal("retained SELECT requires the owning backend recorder context")
	}
	record.Mu.Lock()
	var recorded *Operation
	for _, op := range record.Operations {
		if op.Err == nil && reflect.DeepEqual(op.Columns, []string{"a", "b"}) {
			recorded = op
		}
	}
	if recorded == nil {
		record.Mu.Unlock()
		t.Fatal("retained SELECT has no recorded result")
	}
	capturedRows, closed, closeErr := recorded.Rows, recorded.Closed, recorded.CloseErr
	record.Mu.Unlock()
	captured := make([][]any, 0, len(capturedRows))
	for _, row := range capturedRows {
		if len(row) != 2 {
			t.Fatal("recorded retained row width", row)
		}
		AssertRawValue(t, !native, "INT4", row[0])
		AssertRawValue(t, !native, "TEXT", row[1])
		captured = append(captured, []any{Normalize(row[0]), Normalize(row[1])})
	}
	if !reflect.DeepEqual(captured, want) || !closed || closeErr != nil {
		t.Fatal("recorded retained rows/closure", captured, want, closed, closeErr)
	}
}
