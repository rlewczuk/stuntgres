package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// sequenceArithmeticNullInt64Step checks typed destinations on the runner's
// current numeric row, leaving raw values, recording and cursor ownership intact.
func sequenceArithmeticNullInt64Step(s fixture.Step) fixture.Step {
	s.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		t.Helper()
		if rowIndex < 0 || rowIndex >= len(s.Rows) {
			t.Fatalf("%s: unexpected typed row %d", s.Query, rowIndex)
		}
		want := s.Rows[rowIndex]
		// These arithmetic projections have one or two numeric columns.
		var values [2]sql.NullInt64
		var destinations [2]any
		if len(want) == 0 || len(want) > len(values) {
			t.Fatalf("%s: unexpected numeric column count %d", s.Query, len(want))
		}
		for i := range want {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations[:len(want)]...); err != nil {
			t.Fatal(s.Query, err)
		}
		for i, expected := range want {
			if values[i].Valid != (expected != nil) {
				t.Fatalf("%s: row %d column %d Valid=%t, want %t", s.Query, rowIndex, i, values[i].Valid, expected != nil)
			}
			if expected != nil {
				n, ok := expected.(int64)
				if !ok {
					t.Fatalf("%s: numeric expectation %d has type %T", s.Query, i, expected)
				}
				if values[i].Int64 != n {
					t.Fatalf("%s: row %d column %d Int64=%d, want %d", s.Query, rowIndex, i, values[i].Int64, n)
				}
			}
		}
	}
	return s
}
