package fixture

import (
	"reflect"
	"testing"
)

func LimitLockValues(t *testing.T, outcome RelationOutcome, pg bool, want ...int64) {
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
		t.Fatal("missing lock cursor")
	}
	names, err := outcome.Rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"v"}) {
		t.Fatal(names, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	scan := reflect.TypeFor[int64]()
	if pg {
		scan = reflect.TypeFor[int32]()
	}
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != scan {
		t.Fatal(types, err)
	}
	got := make([]int64, 0)
	for outcome.Rows.Next() {
		var value any
		if err := outcome.Rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		v, ok := value.(int64)
		if !ok {
			t.Fatalf("raw lock payload %T", value)
		}
		got = append(got, v)
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatal(got, want)
		}
	}
	closeQuery := func() error {
		if err := outcome.Rows.Close(); err != nil {
			return err
		}
		if outcome.Stmt != nil {
			return outcome.Stmt.Close()
		}
		return nil
	}
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
}
