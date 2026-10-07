//go:build reference

package reference_test

import "testing"

func referenceViewValues(t *testing.T, outcome relationOutcome, column, typ string, want ...int64) {
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
		t.Fatal("missing view cursor")
	}
	columns, err := outcome.rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != column {
		t.Fatal(columns, err)
	}
	types, err := outcome.rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
		t.Fatal(types, err)
	}
	var got []int64
	for outcome.rows.Next() {
		var value int64
		if err := outcome.rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if err := outcome.rows.Err(); err != nil {
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
}
