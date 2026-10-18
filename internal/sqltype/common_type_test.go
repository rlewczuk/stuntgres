package sqltype

import "testing"

func TestCommonTypeCandidate(t *testing.T) {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	for _, tc := range []struct {
		id        ID
		category  byte
		preferred bool
	}{
		{1007, 'A', false}, {Bool, 'B', true}, {Int4, 'N', false},
		{Oid, 'N', true}, {Regconfig, 'N', false}, {Regdictionary, 'N', false},
		{Text, 'S', true}, {VarChar, 'S', false}, {Name, 'S', false},
		{Float8, 'N', true}, {Inet, 'I', true}, {Cidr, 'I', false},
		{Timestamp, 'D', false}, {Timestamptz, 'D', false}, {Time, 'D', false},
		{Interval, 'T', false}, {UUID, 'U', false},
	} {
		if got := CommonTypeCategory(types[tc.id]); got != tc.category {
			t.Errorf("type %d category=%q want=%q", tc.id, got, tc.category)
		}
		if got := CommonTypePreferred(types[tc.id]); got != tc.preferred {
			t.Errorf("type %d preferred=%t want=%t", tc.id, got, tc.preferred)
		}
	}
	for _, tc := range []struct {
		first, second, want ID
		compatible          bool
	}{
		{Int4, Int8, Int8, true}, {Int8, Int4, Int8, true},
		{Numeric, Float8, Float8, true}, {Float8, Numeric, Float8, true},
		{Cidr, Inet, Inet, true}, {Inet, Cidr, Inet, true},
		{Text, VarChar, Text, true}, {VarChar, Text, VarChar, true},
		{Bool, Int4, Bool, false}, {1007, Int4, 1007, false},
		// Category selection must stay permissive here: ordinary binding
		// owns the later 42846 conversion failure for these temporal pairs.
		{Time, Timestamp, Time, true}, {Timestamp, Time, Timestamp, true},
		{Int4, Int4, Int4, true},
	} {
		got, ok := CommonTypeCandidate(types[tc.first], types[tc.second])
		if ok != tc.compatible || got != types[tc.want] {
			t.Errorf("%d/%d: candidate=%v compatible=%t want=%d/%t", tc.first, tc.second, got, ok, tc.want, tc.compatible)
		}
	}
}
