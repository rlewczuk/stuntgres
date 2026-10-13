package sqltype

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestPGEnumDefinitionMetadata(t *testing.T) {
	members := []EnumMember{{ID: 91, Label: "first", SortOrder: -1}, {ID: 21, Label: "zero", SortOrder: 0}, {ID: math.MaxUint32, Label: "last", SortOrder: 1.5}}
	d, err := EnumWithMembers(45000, "mood", "public.mood", members)
	if err != nil {
		t.Fatal(err)
	}
	members[0].ID, members[0].Label, members[0].SortOrder = 999, "changed", -9
	want := []EnumMember{{ID: 91, Label: "first", SortOrder: -1}, {ID: 21, Label: "zero", SortOrder: 0}, {ID: math.MaxUint32, Label: "last", SortOrder: 1.5}}
	if !reflect.DeepEqual(d.EnumMembers(), want) || d.EnumMemberCount() != len(want) {
		t.Fatal("constructor aliased metadata", d.EnumMembers())
	}
	copy := d.EnumMembers()
	copy[1].SortOrder = -99
	for i, member := range want {
		got, ok := d.EnumMemberAt(i)
		if !ok || got != member {
			t.Fatal("readonly member", i, got, ok)
		}
	}
	for _, i := range []int{-1, len(want)} {
		if got, ok := d.EnumMemberAt(i); ok || got != (EnumMember{}) {
			t.Fatal("member bounds", i, got, ok)
		}
	}
	var absent *Descriptor
	if absent.EnumMemberCount() != 0 {
		t.Fatal("nil count")
	}
	if _, ok := absent.EnumMemberAt(0); ok {
		t.Fatal("nil member")
	}
	if Builtins()[0].EnumMemberCount() != 0 {
		t.Fatal("nonenum count")
	}
	invalid := []struct {
		name    string
		members []EnumMember
	}{
		{"zero_id", []EnumMember{{ID: 0, Label: "a", SortOrder: 1}}},
		{"large_id", []EnumMember{{ID: int64(math.MaxUint32) + 1, Label: "a", SortOrder: 1}}},
		{"duplicate_id", []EnumMember{{ID: 1, Label: "a", SortOrder: 1}, {ID: 1, Label: "b", SortOrder: 2}}},
		{"duplicate_label", []EnumMember{{ID: 1, Label: "a", SortOrder: 1}, {ID: 3, Label: "a", SortOrder: 2}}},
		{"long_label", []EnumMember{{ID: 1, Label: strings.Repeat("x", 64), SortOrder: 1}}},
		{"invalid_utf8", []EnumMember{{ID: 1, Label: "\xff", SortOrder: 1}}},
		{"nul", []EnumMember{{ID: 1, Label: "a\x00b", SortOrder: 1}}},
		{"nan", []EnumMember{{ID: 1, Label: "a", SortOrder: float32(math.NaN())}}},
		{"positive_inf", []EnumMember{{ID: 1, Label: "a", SortOrder: float32(math.Inf(1))}}},
		{"negative_inf", []EnumMember{{ID: 1, Label: "a", SortOrder: float32(math.Inf(-1))}}},
		{"equal_positions", []EnumMember{{ID: 1, Label: "a", SortOrder: 1}, {ID: 3, Label: "b", SortOrder: 1}}},
		{"reverse_positions", []EnumMember{{ID: 1, Label: "a", SortOrder: 2}, {ID: 3, Label: "b", SortOrder: 1}}},
		{"reverse_even_ids", []EnumMember{{ID: 8, Label: "a", SortOrder: 1}, {ID: 2, Label: "b", SortOrder: 2}}},
		{"missing_order_not_rank", []EnumMember{{ID: 1, Label: "a"}, {ID: 3, Label: "b"}}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EnumWithMembers(45001, "bad", "public.bad", tc.members); err == nil {
				t.Fatal("invalid definition admitted", tc.members)
			}
		})
	}
	ctx := context.Background()
	first, _ := d.Input(ctx, "first", -1)
	zero, _ := d.Input(ctx, "zero", -1)
	order, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := order.Compare(ctx, first, zero, InputEnvironment{}); err != nil || cmp != -1 {
		t.Fatal("rank follows definition, not odd member ID", cmp, err)
	}
	float4 := testTypes()[Float4]
	for _, n := range []float32{-1, 0, 1.5, math.Nextafter32(1, 2)} {
		v, err := Float(float4, float64(n))
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Float()
		if err != nil || got != float64(n) {
			t.Fatal("binary32 export", got, n, err)
		}
	}
}
