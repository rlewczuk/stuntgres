package sqltype

import (
	"context"
	"math"
	"testing"
)

func TestGroupingDatumIdentityIsNotGroupingEquality(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	input := func(id ID, text string) Value {
		t.Helper()
		v, err := types[id].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, pair := range [][2]Value{
		{input(Numeric, "1.0"), input(Numeric, "1.00")},
		{String(types[BPChar], "a"), String(types[BPChar], "a ")},
		{input(Float4, "0"), input(Float4, "-0")},
		{input(Float8, "0"), input(Float8, "-0")},
		{{typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000001)}, {typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000002)}},
	} {
		if SameDatum(pair[0], pair[1]) {
			t.Fatal("different constant representations equated", pair)
		}
		grouping, err := BindGrouping(pair[0].Type().Usage())
		if err != nil {
			t.Fatal(err)
		}
		equal, err := grouping.Equal(ctx, pair[0], pair[1])
		if err != nil || !equal {
			t.Fatal("runtime grouping equality changed", equal, err)
		}
		a, err := grouping.Hash(ctx, pair[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := grouping.Hash(ctx, pair[1])
		if err != nil || a != b {
			t.Fatal("runtime hash changed", a, b, err)
		}
	}
	for _, tc := range []struct {
		id   ID
		text string
	}{
		{Bool, "true"}, {Int2, "1"}, {Int4, "2"}, {Int8, "3"},
		{Text, "a"}, {VarChar, "a "}, {BPChar, "a "}, {Numeric, "1.0"},
		{Float4, "-0"}, {Float8, "NaN"}, {Bytea, `\x00ff`},
		{Date, "infinity"}, {Time, "01:00:00"}, {Timestamp, "-infinity"},
		{UUID, "00000000-0000-0000-0000-000000000001"},
	} {
		a, b := input(tc.id, tc.text), input(tc.id, tc.text)
		if !SameDatum(a, b) || !SameDatum(Null(types[tc.id]), Null(types[tc.id])) || SameDatum(a, Null(types[tc.id])) {
			t.Fatal(tc)
		}
	}
	for _, id := range []ID{Name, Regclass} {
		a, b := String(types[id], "0"), String(types[id], "1")
		if !SameDatum(a, a) || SameDatum(a, b) {
			t.Fatal(id)
		}
	}
	foreign := testTypes()
	if SameDatum(input(Int4, "1"), input(Int8, "1")) || SameDatum(input(Text, "a"), String(foreign[Text], "a")) || SameDatum(Value{}, Value{}) {
		t.Fatal("invalid/foreign/type-distinct identity admitted")
	}
}
