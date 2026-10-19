package engine

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestIndexPropertyColumnCapabilityMatrix(t *testing.T) {
	index := &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Desc: false, NullsFirst: true}, {Desc: true, NullsFirst: false}}, Include: []int32{3}, Ready: true, Valid: true, Live: true}
	for _, tc := range []struct {
		ordinal     int64
		property    string
		want, known bool
	}{
		{1, "asc", true, true}, {1, "desc", false, true}, {1, "nulls_first", true, true}, {1, "nulls_last", false, true},
		{2, "asc", false, true}, {2, "desc", true, true}, {2, "nulls_first", false, true}, {2, "nulls_last", true, true},
		{1, "orderable", true, true}, {1, "distance_orderable", false, true}, {1, "returnable", true, true}, {1, "search_array", true, true}, {1, "search_nulls", true, true},
		{3, "asc", false, false}, {3, "desc", false, false}, {3, "nulls_first", false, false}, {3, "nulls_last", false, false},
		{3, "orderable", false, true}, {3, "distance_orderable", false, true}, {3, "returnable", true, true}, {3, "search_array", false, false}, {3, "search_nulls", false, false},
		{0, "asc", false, false}, {-1, "asc", false, false}, {4, "asc", false, false}, {2147483647, "asc", false, false},
		{1, "index_scan", false, false}, {1, "can_order", false, false}, {1, "bogus", false, false},
	} {
		got, known := indexColumnCapability(index, tc.ordinal, tc.property)
		if got != tc.want || known != tc.known {
			t.Fatal(tc, got, known)
		}
	}
}
