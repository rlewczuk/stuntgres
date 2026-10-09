package sqltype

import (
	"context"
	"testing"
)

func TestArrayIOPhysicalVolatility(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{Bool, Bytea, Name, Int2, Int4, Int8, Text, Oid, BPChar, VarChar, Float4, Float8, Numeric, Date, Time, Timetz, Timestamp, Timestamptz, Interval, UUID, Regclass} {
		t.Run(types[id].Name(), func(t *testing.T) {
			array := types[types[id].AssociatedArray()]
			element, err := Domain(60001, "element", "public.element", types[id].Usage())
			if err != nil {
				t.Fatal(err)
			}
			domainArray, err := Array(60002, "_element", element)
			if err != nil {
				t.Fatal(err)
			}
			whole, err := Domain(60003, "whole", "public.whole", array.Usage())
			if err != nil {
				t.Fatal(err)
			}
			chain, err := Domain(60004, "chain", "public.chain", whole.Usage())
			if err != nil {
				t.Fatal(err)
			}
			nested, err := Array(60005, "_whole", whole)
			if err != nil {
				t.Fatal(err)
			}
			wrappedElements, err := Domain(60006, "wrapped_elements", "public.wrapped_elements", domainArray.Usage())
			if err != nil {
				t.Fatal(err)
			}
			for _, descriptor := range []*Descriptor{array, domainArray, whole, chain, nested, wrappedElements} {
				if !descriptor.OutputSessionDependent() {
					t.Fatal("array_out lost STABLE classification", descriptor.Name())
				}
				for _, input := range []bool{false, true} {
					from, to := descriptor.Usage(), types[Text].Usage()
					if input {
						from, to = to, from
					}
					plan, err := PlanCoercion(from, to, Explicit)
					if err != nil || !plan.SessionDependent() {
						t.Fatal("array IO plan lost STABLE classification", descriptor.Name(), input, err)
					}
				}
				for _, mode := range []CoercionContext{Explicit, Assignment} {
					plan, err := PlanCoercion(descriptor.Usage(), descriptor.Usage(), mode)
					if err != nil || plan.SessionDependent() {
						t.Fatal("identity coercion incorrectly classified as IO", descriptor.Name(), mode, err)
					}
				}
			}
		})
	}
}

func TestArrayVolatilityPreservesScalarClassification(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id            ID
		output, input bool
	}{
		{Bool, false, false}, {Bytea, false, false}, {Name, false, false},
		{Int2, false, false}, {Int4, false, false}, {Int8, false, false},
		{Oid, false, false}, {Float4, false, false}, {Float8, false, false},
		{Numeric, false, false}, {UUID, false, false},
		{Date, true, true}, {Time, false, true}, {Timetz, false, true},
		{Timestamp, true, true}, {Timestamptz, true, true}, {Interval, true, true},
	} {
		if got := types[tc.id].OutputSessionDependent(); got != tc.output {
			t.Fatal("scalar output classification changed", tc.id, got)
		}
		for _, input := range []bool{false, true} {
			from, to, want := types[tc.id].Usage(), types[Text].Usage(), tc.output
			if input {
				from, to, want = to, from, tc.input
			}
			// Exercise the existing IO volatility provider without asserting
			// additional scalar conversion-edge admission.
			plan := CoercionPlan{source: from, target: to, context: Explicit, method: viaIO}
			if plan.SessionDependent() != want {
				t.Fatal("scalar IO classification changed", tc.id, input)
			}
		}
	}
	if !types[Regclass].OutputSessionDependent() || types[Text].OutputSessionDependent() {
		t.Fatal("scalar output classification changed")
	}
}

func TestArrayRetagCheckedIdentityAndReownership(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	ctx := context.Background()
	for _, input := range []string{"{}", "[-1:0]={7,NULL}"} {
		value, err := types[1007].Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		same, err := value.Retag(value.Type())
		if err != nil || same != value {
			t.Fatal("exact descriptor rebuilt immutable value", err)
		}
		owned, err := value.Retag(fresh[1007])
		if err != nil || owned.Type() != fresh[1007] || SameDatum(value, owned) {
			t.Fatal("different generation failed reownership", err)
		}
		text, err := owned.Type().Output(ctx, owned)
		if err != nil || text != input {
			t.Fatal("reownership changed bounds, NULLs or element values", text, err)
		}
		for i := range owned.ArrayLen() {
			if owned.ArrayElement(i).Type() != fresh[Int4] || value.ArrayElement(i).Type() != types[Int4] {
				t.Fatal("source mutated or element generation not reowned")
			}
		}
		if value.ArrayLen() != 0 && owned.array == value.array {
			t.Fatal("different element generations shared element buffer")
		}
	}
	null := Null(types[1007])
	if same, err := null.Retag(null.Type()); err != nil || same != null {
		t.Fatal("whole NULL identity changed", err)
	}
	for _, value := range []Value{{}, {typ: types[Unknown]}, {typ: types[1007]}} {
		if _, err := value.Retag(value.Type()); err == nil {
			t.Fatal("exact pointer bypassed validity checks")
		}
	}
	usage := types[1015].Usage()
	usage.Typmod = 7 // varchar(3)
	from, err := Domain(60010, "sized", "public.sized", usage)
	if err != nil {
		t.Fatal(err)
	}
	usage.Type, usage.Typmod = fresh[1015], 8 // varchar(4)
	to, err := Domain(60010, "sized", "public.sized", usage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Null(from).Retag(to); err == nil {
		t.Fatal("same numeric ID bypassed domain constraints")
	}
	fromWhole, err := Domain(60011, "whole", "public.whole", types[1007].Usage())
	if err != nil {
		t.Fatal(err)
	}
	toWhole, err := Domain(60011, "whole", "public.whole", fresh[1007].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"{}", "[-1:0]={7,NULL}"} {
		value, err := fromWhole.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		if same, err := value.Retag(fromWhole); err != nil || same != value {
			t.Fatal("domain wrapper rebuilt exact-pointer payload", err)
		}
		reowned, err := value.Retag(toWhole)
		if err != nil || reowned.Type() != toWhole || SameDatum(value, reowned) {
			t.Fatal("domain wrapper did not reown generation", err)
		}
		text, err := toWhole.Output(ctx, reowned)
		if err != nil || text != input {
			t.Fatal("domain reownership changed bounds, NULLs or element values", text, err)
		}
		for i := range reowned.ArrayLen() {
			if reowned.ArrayElement(i).Type() != fresh[Int4] || value.ArrayElement(i).Type() != types[Int4] {
				t.Fatal("domain wrapper mutated source or retained foreign elements")
			}
		}
	}
	for _, target := range []*Descriptor{nil, types[Unknown], types[1009], types[Int4]} {
		if _, err := Null(types[1007]).Retag(target); err == nil {
			t.Fatal("invalid target identity accepted", target)
		}
	}
}
