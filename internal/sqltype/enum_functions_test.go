package sqltype

import (
	"context"
	"testing"
)

func TestEnumSupportFunctions(t *testing.T) {
	ctx := context.Background()
	mood, err := Enum(45000, "mood", "public.mood", []string{"z", "a", "m"})
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(45001, "_mood", mood)
	if err != nil {
		t.Fatal(err)
	}
	input := func(label string) Value {
		v, err := mood.Input(ctx, label, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		name string
		args []Value
		want string
	}{
		{"enum_first", []Value{Null(mood)}, "z"}, {"enum_last", []Value{input("a")}, "m"},
		{"enum_range", []Value{Null(mood)}, "{z,a,m}"}, {"enum_range", []Value{input("z"), input("a")}, "{z,a}"},
		{"enum_range", []Value{Null(mood), input("a")}, "{z,a}"}, {"enum_range", []Value{input("a"), Null(mood)}, "{a,m}"},
		{"enum_range", []Value{Null(mood), Null(mood)}, "{z,a,m}"}, {"enum_range", []Value{input("a"), input("a")}, "{a}"},
		{"enum_range", []Value{input("m"), input("z")}, "{}"},
	} {
		result := mood
		if tc.name == "enum_range" {
			result = array
		}
		usages := make([]Usage, len(tc.args))
		for i := range usages {
			usages[i] = mood.Usage()
		}
		plan, err := BindEnumSupport(tc.name, result.Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		var values [2]Value
		copy(values[:], tc.args)
		got, err := plan.Evaluate(ctx, values[0], values[1], InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := result.Output(ctx, got)
		if err != nil || text != tc.want || got.IsNull() || got.Type() != result {
			t.Fatal(text, got, err)
		}
		if result == array {
			for i := range got.ArrayLen() {
				if got.ArrayElement(i).Type() != mood || got.ArrayElement(i).IsNull() {
					t.Fatal("foreign range cell")
				}
			}
			if got.ArrayLen() == 0 && len(got.ArrayDimensions()) != 0 {
				t.Fatal("empty range rank")
			}
		}
	}
	first, _ := BindEnumSupport("enum_first", mood.Usage(), mood.Usage())
	if allocations := testing.AllocsPerRun(100, func() {
		v, err := first.Evaluate(ctx, Null(mood), Value{}, InputEnvironment{})
		if err != nil || v.Type() != mood {
			panic("enum first")
		}
	}); allocations != 0 {
		t.Fatal("scalar allocations", allocations)
	}
	foreign, _ := Enum(mood.ID(), "mood", "public.mood", mood.EnumLabels())
	if _, err := first.Evaluate(ctx, Null(foreign), Value{}, InputEnvironment{}); err == nil {
		t.Fatal("foreign generation accepted")
	}
	if _, err := BindEnumSupport("enum_range", array.Usage(), foreign.Usage()); err == nil {
		t.Fatal("foreign array link accepted")
	}
	empty, _ := Enum(45002, "empty", "public.empty", nil)
	emptyArray, _ := Array(45003, "_empty", empty)
	emptyRange, _ := BindEnumSupport("enum_range", emptyArray.Usage(), empty.Usage())
	v, err := emptyRange.Evaluate(ctx, Null(empty), Value{}, InputEnvironment{})
	if err != nil || v.IsNull() || v.ArrayLen() != 0 || len(v.ArrayDimensions()) != 0 {
		t.Fatal(v, err)
	}
	for _, name := range []string{"enum_first", "enum_last"} {
		p, _ := BindEnumSupport(name, empty.Usage(), empty.Usage())
		if _, err := p.Evaluate(ctx, Null(empty), Value{}, InputEnvironment{}); err == nil {
			t.Fatal("empty extrema")
		}
	}
	special, _ := Enum(45004, "special", "public.special", []string{"", "NULL", "x,y", "{x}", "a\"b", "a\\b", " a "})
	specialArray, _ := Array(45005, "_special", special)
	p, _ := BindEnumSupport("enum_range", specialArray.Usage(), special.Usage())
	v, err = p.Evaluate(ctx, Null(special), Value{}, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := specialArray.Output(ctx, v)
	if err != nil || text != `{"","NULL","x,y","{x}","a\"b","a\\b"," a "}` {
		t.Fatal(text, err)
	}
	dimensions := v.ArrayDimensions()
	dimensions[0].Length = 99
	if v.ArrayDimensions()[0].Length != 7 {
		t.Fatal("mutable range dimensions")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Evaluate(canceled, Null(special), Value{}, InputEnvironment{}); err != context.Canceled {
		t.Fatal(err)
	}
}
