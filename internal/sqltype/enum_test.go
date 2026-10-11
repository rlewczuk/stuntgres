package sqltype

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestEnumImmutableCodecAndOrdering(t *testing.T) {
	ctx := context.Background()
	labels := []string{"sad", "ok", "happy", "", " O'\\K "}
	mood, err := Enum(45000, "mood", "public.mood", labels)
	if err != nil {
		t.Fatal(err)
	}
	labels[0] = "changed"
	detached := mood.EnumLabels()
	detached[1] = "changed"
	if !reflect.DeepEqual(mood.EnumLabels(), []string{"sad", "ok", "happy", "", " O'\\K "}) {
		t.Fatal("mutable labels escaped")
	}
	order, err := BindOrdering(mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i, label := range mood.EnumLabels() {
		value, err := mood.Input(ctx, label, -1)
		if err != nil {
			t.Fatal(err)
		}
		text, err := mood.Output(ctx, value)
		if err != nil || text != label {
			t.Fatal(text, err)
		}
		literal, err := mood.SQLLiteral(ctx, value)
		if err != nil || literal != "'"+strings.ReplaceAll(label, "'", "''")+"'" {
			t.Fatal(literal, err)
		}
		for j, other := range mood.EnumLabels() {
			right, err := mood.Input(ctx, other, -1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := order.Compare(ctx, value, right, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Fatal(i, j, got, want)
			}
		}
	}
	for _, text := range []string{"Sad", "sad ", strings.Repeat("x", 64)} {
		if _, err := mood.Input(ctx, text, -1); err == nil {
			t.Fatal("invalid label accepted", text)
		}
	}
	for _, ordinal := range []int64{0, int64(len(labels)) + 1} {
		if _, err := mood.Output(ctx, Value{typ: mood, integer: ordinal}); err == nil {
			t.Fatal("invalid ordinal accepted")
		}
	}
	empty, err := Enum(45001, "empty", "public.empty", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Input(ctx, "", -1); err == nil {
		t.Fatal("empty enum accepted value")
	}
	if !Null(empty).IsNull() || Null(empty).Type() != empty {
		t.Fatal("NULL lost enum identity")
	}
	if _, err := BindOrdering(empty.Usage()); err != nil {
		t.Fatal("empty enum lacks ordering", err)
	}
}

func TestEnumIdentityCoercionAndGeneration(t *testing.T) {
	ctx := context.Background()
	a, _ := Enum(45000, "a", "public.a", []string{"z", "a"})
	b, _ := Enum(45001, "b", "public.b", []string{"z", "a"})
	generation, _ := Enum(a.ID(), "a", "renamed.a", []string{"z", "a"})
	foreignMembers, _ := EnumWithMembers(a.ID(), "a", "public.a", []EnumMember{{ID: 100, Label: "a"}, {ID: 101, Label: "z"}})
	value, _ := a.Input(ctx, "z", -1)
	if _, err := BindComparison(a, b, CollationRef{}, false); err == nil {
		t.Fatal("distinct enums compare")
	}
	if _, err := value.Retag(b); err == nil {
		t.Fatal("distinct enum retag")
	}
	if _, err := value.Retag(foreignMembers); err == nil {
		t.Fatal("foreign enum member retag")
	}
	retagged, err := value.Retag(generation)
	if err != nil || retagged.Type() != generation {
		t.Fatal(err)
	}
	if _, err := a.Output(ctx, retagged); err == nil {
		t.Fatal("foreign value accepted")
	}
	mapped, err := value.RemapEnum(b)
	if err != nil || mapped.Type() != b {
		t.Fatal(err)
	}
	text := testTypes()[Text]
	for _, context := range []CoercionContext{Implicit, Assignment} {
		if _, err := PlanCoercion(text.Usage(), a.Usage(), context); err == nil {
			t.Fatal("implicit text input")
		}
	}
	for _, pair := range [][2]*Descriptor{{a, text}, {text, a}} {
		p, err := PlanCoercion(pair[0].Usage(), pair[1].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		source, err := pair[0].Input(ctx, "z", -1)
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
		if err != nil || result.Type() != pair[1] {
			t.Fatal(err)
		}
	}
	base, err := Domain(45002, "d", "public.d", a.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := base.Input(ctx, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := base.Output(ctx, wrapped); err != nil || output != "a" {
		t.Fatal(output, err)
	}
	comparison, _ := BindComparison(a, a, CollationRef{}, false)
	right, _ := a.Input(ctx, "a", -1)
	if allocs := testing.AllocsPerRun(100, func() {
		if got, err := comparison.Compare(ctx, value, right, InputEnvironment{}); err != nil || got != -1 {
			panic("enum comparison")
		}
	}); allocs != 0 {
		t.Fatal("enum comparison allocations", allocs)
	}
	for operator := CompareEqual; operator <= CompareGreaterEqual; operator++ {
		predicate, err := BindComparisonPredicate(operator, a, a, testTypes()[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		result, err := predicate.Evaluate(ctx, value, right, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := result.Boolean()
		if err != nil || truth != []bool{false, true, true, true, false, false}[operator] {
			t.Fatal(operator, truth, err)
		}
		null, err := predicate.Evaluate(ctx, Null(a), right, InputEnvironment{})
		if err != nil || !null.IsNull() {
			t.Fatal(operator, null, err)
		}
	}
}

func TestEnumDomainComparisonAndOrdering(t *testing.T) {
	ctx := context.Background()
	mood, err := Enum(45000, "mood", "public.mood", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := Domain(45001, "wrapped", "public.wrapped", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	other, err := Domain(45002, "other", "public.other", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*Descriptor{{wrapped, mood}, {mood, wrapped}, {wrapped, wrapped}, {wrapped, other}} {
		for operator := CompareEqual; operator <= CompareGreaterEqual; operator++ {
			if _, err := BindComparisonPredicate(operator, pair[0], pair[1], testTypes()[Bool], CollationRef{}, false); err == nil {
				t.Fatal("domain accepted as polymorphic enum operand", pair[0].Name(), pair[1].Name(), operator)
			}
		}
	}
	order, err := BindOrdering(wrapped.Usage())
	if err != nil {
		t.Fatal(err)
	}
	left, err := wrapped.Input(ctx, "z", -1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := wrapped.Input(ctx, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := order.Compare(ctx, left, right, InputEnvironment{}); err != nil || got != -1 {
		t.Fatal("domain ordering lost declaration order", got, err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if got, err := order.Compare(ctx, left, right, InputEnvironment{}); err != nil || got != -1 {
			panic("domain enum ordering")
		}
	}); allocs != 0 {
		t.Fatal("domain enum ordering allocations", allocs)
	}
	baseValue, err := mood.Input(ctx, "z", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := order.Compare(ctx, baseValue, right, InputEnvironment{}); err == nil {
		t.Fatal("domain ordering accepted foreign datum identity")
	}
	integer := testTypes()[Int4]
	integerDomain, err := Domain(45003, "integer_domain", "public.integer_domain", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*Descriptor{{integerDomain, integer}, {integer, integerDomain}, {integerDomain, integerDomain}} {
		if _, err := BindComparison(pair[0], pair[1], CollationRef{}, false); err != nil {
			t.Fatal("nonenum domain comparison changed", err)
		}
	}
}
