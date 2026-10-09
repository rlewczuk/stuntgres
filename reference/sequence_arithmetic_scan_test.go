package reference_test

import (
	"database/sql"
	"testing"
)

// sequenceArithmeticNullInt64Step checks typed destinations on the runner's
// current numeric row, leaving raw values, recording and cursor ownership intact.
func sequenceArithmeticNullInt64Step(s referenceStep) referenceStep {
	s.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		t.Helper()
		if rowIndex < 0 || rowIndex >= len(s.rows) {
			t.Fatalf("%s: unexpected typed row %d", s.query, rowIndex)
		}
		want := s.rows[rowIndex]
		// These arithmetic projections have one or two numeric columns.
		var values [2]sql.NullInt64
		var destinations [2]any
		if len(want) == 0 || len(want) > len(values) {
			t.Fatalf("%s: unexpected numeric column count %d", s.query, len(want))
		}
		for i := range want {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations[:len(want)]...); err != nil {
			t.Fatal(s.query, err)
		}
		for i, expected := range want {
			if values[i].Valid != (expected != nil) {
				t.Fatalf("%s: row %d column %d Valid=%t, want %t", s.query, rowIndex, i, values[i].Valid, expected != nil)
			}
			if expected != nil {
				n, ok := expected.(int64)
				if !ok {
					t.Fatalf("%s: numeric expectation %d has type %T", s.query, i, expected)
				}
				if values[i].Int64 != n {
					t.Fatalf("%s: row %d column %d Int64=%d, want %d", s.query, rowIndex, i, values[i].Int64, n)
				}
			}
		}
	}
	return s
}
