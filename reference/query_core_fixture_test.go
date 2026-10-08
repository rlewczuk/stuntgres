package reference_test

import (
	"reflect"
	"testing"
)

// These predetermined row sets exercise the actual recording assertion used by
// referenceRunStep, independently of database execution order or middleware.
func TestReferenceQueryCoreRecordedRowComparison(t *testing.T) {
	for _, tc := range []struct {
		name               string
		bag                bool
		captured, expected [][]any
		match              bool
	}{
		{"bag distinguishable permutation", true, [][]any{{int64(4)}, {int64(1)}}, [][]any{{int64(1)}, {int64(4)}}, true},
		{"ordered permutation rejected", false, [][]any{{int64(4)}, {int64(1)}}, [][]any{{int64(1)}, {int64(4)}}, false},
		{"ordered matching duplicates", false, [][]any{{int64(1)}, {int64(1)}, {int64(4)}}, [][]any{{int64(1)}, {int64(1)}, {int64(4)}}, true},
		{"bag duplicate permutation", true, [][]any{{int64(4)}, {int64(1)}, {int64(1)}}, [][]any{{int64(1)}, {int64(1)}, {int64(4)}}, true},
		{"bag wrong duplicate multiplicity", true, [][]any{{int64(1)}, {int64(4)}, {int64(4)}}, [][]any{{int64(1)}, {int64(1)}, {int64(4)}}, false},
		{"bag missing duplicate", true, [][]any{{int64(1)}}, [][]any{{int64(1)}, {int64(1)}}, false},
		{"bag NULL permutation", true, [][]any{{nil}, {nil}, {int64(1)}}, [][]any{{nil}, {int64(1)}, {nil}}, true},
		{"bag wrong NULL multiplicity", true, [][]any{{nil}, {int64(1)}, {int64(1)}}, [][]any{{nil}, {int64(1)}, {nil}}, false},
		{"bag zero width occurrences", true, [][]any{{}, {}}, [][]any{{}, {}}, true},
		{"bag missing zero width occurrence", true, [][]any{{}}, [][]any{{}, {}}, false},
		{"bag extra zero width occurrence", true, [][]any{{}, {}, {}}, [][]any{{}, {}}, false},
		{"bag zero width is not NULL", true, [][]any{{nil}}, [][]any{{}}, false},
		{"bag empty relation", true, [][]any{}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := referenceStep{bag: tc.bag, rows: tc.expected}
			if got := referenceRecordedRowsMatch(tc.captured, step); got != tc.match {
				t.Fatalf("recorded-row comparison bag=%v captured=%#v expected=%#v: got %v want %v", tc.bag, tc.captured, tc.expected, got, tc.match)
			}
			compared, matched := referenceCompareRows(tc.captured, tc.expected, tc.bag)
			if matched != tc.match {
				t.Fatalf("consumed-row comparison: got %v want %v", matched, tc.match)
			}
			if matched {
				expected := tc.expected
				if expected == nil {
					expected = make([][]any, 0)
				}
				if !reflect.DeepEqual(compared, expected) {
					t.Fatalf("canonical rows got %#v want %#v", compared, expected)
				}
			}
		})
	}
}
