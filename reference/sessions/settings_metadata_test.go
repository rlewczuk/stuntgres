package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func settingsIntegralMetadata(t *testing.T, rows *sql.Rows, postgres bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	scan := reflect.TypeFor[int64]()
	if postgres {
		scan = reflect.TypeFor[int32]()
	}
	n, known := types[0].Nullable()
	if types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != scan || known == postgres || n {
		t.Fatal(types[0], n, known)
	}
	if length, known := types[0].Length(); known || length != 0 {
		t.Fatal("INT4 length", length, known)
	}
}

func settingsTextMetadata(t *testing.T, rows *sql.Rows, postgres, nullable bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	n, known := types[0].Nullable()
	if types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeFor[string]() || known == postgres || (!postgres && n != nullable) || (postgres && n) {
		t.Fatal(types[0], n, known)
	}
	if length, known := types[0].Length(); !known || length != math.MaxInt64 {
		t.Fatal("TEXT unbounded length", length, known)
	}
	if precision, scale, known := types[0].DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("TEXT decimal size", precision, scale, known)
	}
}
