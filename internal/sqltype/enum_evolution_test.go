package sqltype

import (
	"context"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestEnumMemberEvolution(t *testing.T) {
	ctx := context.Background()
	members := []EnumMember{{ID: 90, Label: "z"}, {ID: 20, Label: "q"}}
	old, err := EnumWithMembers(45000, "mood", "public.mood", members)
	if err != nil {
		t.Fatal(err)
	}
	members[0].Label = "mutated"
	next, err := EnumWithMembers(old.ID(), "mood", "public.mood", []EnumMember{{ID: 90, Label: "z"}, {ID: 100, Label: "a"}, {ID: 20, Label: "end"}})
	if err != nil {
		t.Fatal(err)
	}
	z, _ := old.Input(ctx, "z", -1)
	q, _ := old.Input(ctx, "q", -1)
	end, err := q.Retag(next)
	if err != nil || end.integer != q.integer {
		t.Fatal(end, err)
	}
	if out, err := next.Output(ctx, end); err != nil || out != "end" {
		t.Fatal(out, err)
	}
	if out, err := old.Output(ctx, q); err != nil || out != "q" {
		t.Fatal("old snapshot", out, err)
	}
	zNew, _ := z.Retag(next)
	order, _ := BindOrdering(next.Usage())
	if cmp, err := order.Compare(ctx, zNew, end, InputEnvironment{}); err != nil || cmp != -1 {
		t.Fatal("rank must not be member ID", cmp, err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if _, err := order.Compare(ctx, zNew, end, InputEnvironment{}); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("scalar comparison allocation", allocations)
	}
	copy := next.EnumMembers()
	copy[0].ID = 999
	if !reflect.DeepEqual(next.EnumMembers(), []EnumMember{{ID: 90, Label: "z"}, {ID: 100, Label: "a"}, {ID: 20, Label: "end"}}) {
		t.Fatal("mutable members escaped")
	}
	for _, invalid := range [][]EnumMember{{{ID: 0, Label: "z"}}, {{ID: 1, Label: "z"}, {ID: 1, Label: "q"}}, {{ID: 1, Label: "z"}, {ID: 2, Label: "z"}}} {
		if _, err := EnumWithMembers(45001, "bad", "public.bad", invalid); err == nil {
			t.Fatal("invalid member set admitted", invalid)
		}
	}
}

func TestEnumMemberRetagImport(t *testing.T) {
	ctx := context.Background()
	source, _ := EnumWithMembers(45000, "mood", "public.mood", []EnumMember{{ID: 77, Label: "old"}})
	renamed, _ := EnumWithMembers(source.ID(), "mood", "public.mood", []EnumMember{{ID: 77, Label: "new"}})
	foreign, _ := EnumWithMembers(source.ID(), "mood", "public.mood", []EnumMember{{ID: 88, Label: "old"}})
	imported, _ := EnumWithMembers(45001, "mood", "public.mood", []EnumMember{{ID: 99, Label: "old"}})
	v, _ := source.Input(ctx, "old", -1)
	if _, err := v.Retag(renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Retag(foreign); err == nil {
		t.Fatal("foreign member retag")
	}
	if _, err := v.Retag(imported); err == nil {
		t.Fatal("cross-type retag")
	}
	mapped, err := v.RemapEnum(imported)
	if err != nil || mapped.integer != 99 {
		t.Fatal(mapped, err)
	}
	if _, err := v.RemapEnum(renamed); err == nil {
		t.Fatal("import missing spelling admitted")
	}
	if n, err := Null(source).Retag(renamed); err != nil || !n.IsNull() {
		t.Fatal(n, err)
	}
}

func TestEnumProducerSafety(t *testing.T) {
	ctx := context.Background()
	mood, _ := EnumWithMembers(45000, "mood", "public.mood", []EnumMember{{ID: 90, Label: "old"}, {ID: 10, Label: "new"}})
	array, _ := Array(45001, "_mood", mood)
	env := InputEnvironment{
		EnumMemberSafe: func(id ID, member int64) bool { return id == mood.ID() && member != 10 },
		TypeDiagnosticName: func(id ID) string {
			if id == mood.ID() { return "mood" }
			return ""
		},
	}
	if _, err := mood.InputWithEnvironment(ctx, "old", -1, env); err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "55P04" || d.Message != `unsafe use of new value "new" of enum type mood` || d.Hint != "New enum values must be committed before they can be used." {
			t.Fatal(err)
		}
	}
	_, err := mood.InputWithEnvironment(ctx, "new", -1, env)
	check(err)
	first, _ := BindEnumSupport("enum_first", mood.Usage(), mood.Usage())
	if _, err := first.Evaluate(ctx, Null(mood), Value{}, env); err != nil {
		t.Fatal(err)
	}
	last, _ := BindEnumSupport("enum_last", mood.Usage(), mood.Usage())
	_, err = last.Evaluate(ctx, Null(mood), Value{}, env)
	check(err)
	rangePlan, _ := BindEnumSupport("enum_range", array.Usage(), mood.Usage(), mood.Usage())
	old, _ := mood.Input(ctx, "old", -1)
	if v, err := rangePlan.Evaluate(ctx, old, old, env); err != nil || v.ArrayLen() != 1 {
		t.Fatal(v, err)
	}
	_, err = rangePlan.Evaluate(ctx, old, Null(mood), env)
	check(err)
}
