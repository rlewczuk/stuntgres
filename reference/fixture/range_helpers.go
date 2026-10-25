package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func RangeConsumeHeld(t *testing.T, rows *sql.Rows, check Step) {
	t.Helper()
	defer rows.Close()
	if check.CheckMetadata != nil {
		check.CheckMetadata(t, rows)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if typ.Name() != check.Columns[i] || typ.DatabaseTypeName() != check.Types[i] {
			t.Fatal("held column changed", i, typ)
		}
	}
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(check.Columns))
		dst := make([]any, len(row))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if check.CheckRaw == nil || !check.CheckRaw(t, i, len(got), value) {
				AssertRawValue(t, false, check.Types[i], value)
			}
			row[i] = Normalize(value)
		}
		got = append(got, row)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, check.Rows) {
		t.Fatalf("held rows=%#v want=%#v err=%v", got, check.Rows, rows.Err())
	}
}
