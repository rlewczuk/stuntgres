package reference_test

import (
	"reflect"
	"testing"
)

func referenceLimitLockValues(t *testing.T, outcome relationOutcome, pg bool, want ...int64) {
	t.Helper()
	if outcome.stmt != nil {
		defer outcome.stmt.Close()
	}
	if outcome.rows != nil {
		defer outcome.rows.Close()
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.rows == nil {
		t.Fatal("missing lock cursor")
	}
	names, err := outcome.rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"v"}) {
		t.Fatal(names, err)
	}
	types, err := outcome.rows.ColumnTypes()
	scan := reflect.TypeFor[int64]()
	if pg {
		scan = reflect.TypeFor[int32]()
	}
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != scan {
		t.Fatal(types, err)
	}
	got := make([]int64, 0)
	for outcome.rows.Next() {
		var value any
		if err := outcome.rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		v, ok := value.(int64)
		if !ok {
			t.Fatalf("raw lock payload %T", value)
		}
		got = append(got, v)
	}
	if err := outcome.rows.Err(); err != nil {
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
		if err := outcome.rows.Close(); err != nil {
			return err
		}
		if outcome.stmt != nil {
			return outcome.stmt.Close()
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
