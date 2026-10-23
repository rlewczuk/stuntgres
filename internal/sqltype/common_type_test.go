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

func TestSelectCommonUsage(t *testing.T) {
	types := testTypes()
	domain, err := Domain(92000, "common_domain", "common_domain", types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	modified := types[VarChar].Usage()
	modified.Typmod = 7
	for _, tc := range []struct {
		name   string
		inputs []Usage
		want   Usage
	}{
		{"empty", nil, types[Text].Usage()},
		{"unknown", []Usage{{}, {}}, types[Text].Usage()},
		{"numeric", []Usage{types[Int4].Usage(), types[Int8].Usage()}, types[Int8].Usage()},
		{"same_domain", []Usage{domain.Usage(), domain.Usage()}, domain.Usage()},
		{"domain_base", []Usage{domain.Usage(), types[Int4].Usage()}, types[Int4].Usage()},
		{"same_modifier", []Usage{modified, modified}, modified},
		{"unknown_modifier", []Usage{modified, {}}, types[VarChar].Usage()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, mismatch := SelectCommonUsage(len(tc.inputs), func(i int) Usage { return tc.inputs[i] }, types[Text].Usage(), nil)
			if mismatch.Left != nil || got != tc.want {
				t.Fatalf("common=%v/%v want=%v", got, mismatch, tc.want)
			}
		})
	}
	inputs := []Usage{types[Int4].Usage(), types[Int8].Usage(), types[Bool].Usage()}
	got, mismatch := SelectCommonUsage(len(inputs), func(i int) Usage { return inputs[i] }, types[Text].Usage(), nil)
	if got != (Usage{}) || mismatch.Left == nil || mismatch.Index != 2 || mismatch.Left != types[Int8] || mismatch.Right != types[Bool] {
		t.Fatalf("lost diagnostic evidence: %v/%v", got, mismatch)
	}
}
