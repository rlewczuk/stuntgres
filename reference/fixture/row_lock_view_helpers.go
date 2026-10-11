package fixture

import (
	"reflect"
	"testing"
)

func ViewValues(t *testing.T, outcome RelationOutcome, column, typ string, want ...int64) {
	t.Helper()
	if outcome.Stmt != nil {
		defer outcome.Stmt.Close()
	}
	if outcome.Rows != nil {
		defer outcome.Rows.Close()
	}
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if outcome.Rows == nil {
		t.Fatal("missing view cursor")
	}
	columns, err := outcome.Rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != column {
		t.Fatal(columns, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
		t.Fatal(types, err)
	}
	if outcome.RawBackend != RawLegacy {
		scan := reflect.TypeFor[int64]()
		if typ == "INT4" && outcome.RawBackend == RawPostgres {
			scan = reflect.TypeFor[int32]()
		}
		if types[0].ScanType() != scan {
			t.Fatalf("%s ScanType %v, want %v", typ, types[0].ScanType(), scan)
		}
	}
	var got []int64
	for outcome.Rows.Next() {
		var value any
		if err := outcome.Rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		n, ok := value.(int64)
		if !ok {
			t.Fatalf("raw %s payload %T, want int64", typ, value)
		}
		got = append(got, n)
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatal(got, want)
		}
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.Stmt != nil {
		if err := outcome.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	if outcome.Stmt != nil {
		if err := outcome.Stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
