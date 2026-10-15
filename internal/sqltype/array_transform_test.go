package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestArrayTransformKernels(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	array := types[1007]
	input := func(s string) Value { return arraySearchUnitInput(t, array, s) }
	check := func(v Value, err error, want string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		text, err := v.Type().Output(ctx, v)
		if err != nil || text != want || v.Type() != array {
			t.Fatalf("got %q want %q: %v", text, want, err)
		}
	}
	a := input("[-1:1][5:6]={{1,2},{3,NULL},{5,6}}")
	v, err := ArrayReverse(ctx, a)
	check(v, err, "[-1:1][5:6]={{5,6},{3,NULL},{1,2}}")
	if v.array == a.array || &v.array.elements[0] == &a.array.elements[0] {
		t.Fatal("changed result retained mutable element buffer")
	}
	v, err = ArrayTrim(ctx, a, 1)
	check(v, err, "{{1,2},{3,NULL}}")
	v, err = ArrayTrim(ctx, a, 0)
	check(v, err, "{{1,2},{3,NULL},{5,6}}")
	if v.array == a.array {
		t.Fatal("trim failed to rebase")
	}
	v, err = ArrayTrim(ctx, a, 3)
	check(v, err, "{}")
	if v.array.elements != nil || v.array.dimensions != nil {
		t.Fatal("empty retained backing buffers")
	}
	v, err = ArrayCat(ctx, input("[5:6]={1,2}"), input("[-2:-1][5:6]={{3,4},{5,6}}"))
	check(v, err, "[-2:0][5:6]={{1,2},{3,4},{5,6}}")
	seven, _ := Integer(types[Int4], 7)
	v, err = ArrayFill(ctx, array, seven, input("{2,3}"), nil)
	check(v, err, "{{7,7,7},{7,7,7}}")
	bounds := input("{-1,5}")
	v, err = ArrayFill(ctx, array, seven, input("{2,3}"), &bounds)
	check(v, err, "[-1:0][5:7]={{7,7,7},{7,7,7}}")
	v, err = ArrayPush(ctx, input("[0:1]={2,3}"), seven, true)
	check(v, err, "[0:2]={7,2,3}")
	_, err = ArrayPush(ctx, input("[-2147483648:-2147483648]={1}"), seven, true)
	arraySearchUnitState(t, err, "22003", "integer out of range")
	for _, s := range []string{"{-1}", "{0,-1}", "{-1,0}", "{2147483647,2}"} {
		_, err = ArrayFill(ctx, array, seven, input(s), nil)
		arraySearchUnitState(t, err, "54000", "array size exceeds the maximum allowed (134217727)")
	}
	bounds = input("{2147483647}")
	v, err = ArrayFill(ctx, array, seven, input("{0}"), &bounds)
	check(v, err, "{}")
	_, err = ArrayFill(ctx, array, seven, input("{2}"), &bounds)
	arraySearchUnitState(t, err, "54000", "array lower bound is too large: 2147483647")
	check(a, nil, "[-1:1][5:6]={{1,2},{3,NULL},{5,6}}")
	// No-op paths retain immutable payload and allocate no replacement buffer.
	singleton := input("{1}")
	plan, err := BindArraySort(array, array.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []func() (Value, error){func() (Value, error) { return ArrayReverse(ctx, singleton) }, func() (Value, error) { return ArrayTrim(ctx, singleton, 0) }, func() (Value, error) { return ArrayCat(ctx, singleton, Null(array)) }, func() (Value, error) { return plan.Evaluate(ctx, singleton, false, false, InputEnvironment{}) }} {
		v, err := f()
		if err != nil || v.array != singleton.array {
			t.Fatal("no-op copied payload", err)
		}
		if n := testing.AllocsPerRun(20, func() {
			if _, err := f(); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("no-op allocations", n)
		}
	}
}

func TestArrayTransformSortAuthority(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	array := types[1007]
	plan, err := BindArraySort(array, array.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source, want string
		desc, first  bool
	}{
		{"{3,NULL,1,2,NULL}", "{1,2,3,NULL,NULL}", false, false},
		{"{3,NULL,1,2,NULL}", "{NULL,NULL,3,2,1}", true, true},
		{"{3,NULL,1,2,NULL}", "{3,2,1,NULL,NULL}", true, false},
		{"{3,NULL,1,2,NULL}", "{NULL,NULL,1,2,3}", false, true},
		{"[10:12][20:21]={{1,2},{10,20},{3,4}}", "[10:12][20:21]={{1,2},{3,4},{10,20}}", false, false},
		{"{{1,NULL},{1,2},{NULL,0}}", "{{1,2},{1,NULL},{NULL,0}}", false, true},
	} {
		a := arraySearchUnitInput(t, array, tc.source)
		v, err := plan.Evaluate(ctx, a, tc.desc, tc.first, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := array.Output(ctx, v)
		if err != nil || text != tc.want {
			t.Fatal(text, err)
		}
		text, err = array.Output(ctx, a)
		if err != nil || text != tc.source {
			t.Fatal("mutated input", text, err)
		}
	}
	json := types[types[JSON].AssociatedArray()]
	jp, err := BindArraySort(json, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, code string }{{"{NULL,NULL}", "0A000"}, {"{{NULL},{NULL}}", "42883"}} {
		_, err := jp.Evaluate(ctx, arraySearchUnitInput(t, json, tc.source), false, false, InputEnvironment{})
		arraySearchUnitState(t, err, tc.code, "could not identify a comparison function for type json")
	}
	for _, s := range []string{"{}", "{NULL}"} {
		a := arraySearchUnitInput(t, json, s)
		v, err := jp.Evaluate(ctx, a, false, false, InputEnvironment{})
		if err != nil || v.array != a.array {
			t.Fatal(v, err)
		}
	}
	composite := compositeForTest(t, 60200, CompositeMember{1, "n", types[Int4].Usage()})
	composites, err := Array(60201, "_record_type", composite)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := BindArraySort(composites, CollationRef{}, false)
	if err != nil {
		t.Fatal("shape-only composite sort demanded comparison at bind", err)
	}
	rebuilt, err := BindArraySort(composites, CollationRef{}, false)
	if err != nil || !cp.Equal(rebuilt) {
		t.Fatal("composite sort lost generation-owned authority", err)
	}
	for _, source := range []string{"{}", `{"(2)"}`, `{{"(2)","(1)"}}`} {
		a := arraySearchUnitInput(t, composites, source)
		v, err := cp.Evaluate(ctx, a, false, false, InputEnvironment{})
		if err != nil || v.array != a.array {
			t.Fatal("shape-only composite sort copied or demanded comparison", source, err)
		}
	}
	for _, source := range []string{`{"(2)","(1)"}`, "{NULL,NULL}", `{{"(2)"},{"(1)"}}`} {
		_, err := cp.Evaluate(ctx, arraySearchUnitInput(t, composites, source), false, false, InputEnvironment{})
		arraySearchUnitState(t, err, "0A000", "composite comparison is unsupported")
	}
	// The ordinary comparison boundary is not broadened by sort's demand timing.
	_, err = BindComparison(composite, composite, CollationRef{}, false)
	arraySearchUnitState(t, err, "0A000", "composite comparison is unsupported")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	a := arraySearchUnitInput(t, array, "{2,1}")
	if _, err := plan.Evaluate(canceled, a, false, false, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Comparator failures are latched before an output Value can escape.
	sorter := arrayBlockSorter{ctx: canceled, values: []Value{a.ArrayElement(0), a.ArrayElement(1)}, width: 1, leaf: plan.leaf}
	if sorter.Less(0, 1) || !errors.Is(sorter.err, context.Canceled) {
		t.Fatal(sorter.err)
	}
}

func TestArrayTransformDomainPayload(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	domain, err := Domain(60101, "vector", "public.vector", types[1007].Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(60102, "_vector", domain)
	if err != nil {
		t.Fatal(err)
	}
	physical := arraySearchUnitInput(t, types[1007], "{1,2}")
	leaf, err := domain.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	lengths := arraySearchUnitInput(t, types[1007], "{2}")
	filled, err := ArrayFill(ctx, array, leaf, lengths, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range filled.ArrayLen() {
		if filled.ArrayElement(i).Type() != domain || filled.ArrayElement(i).array != leaf.array {
			t.Fatal("fill copied immutable domain leaf or lost identity")
		}
	}
	pushed, err := ArrayPush(ctx, filled, Null(domain), false)
	if err != nil || pushed.ArrayLen() != 3 || !pushed.ArrayElement(2).IsNull() || pushed.ArrayElement(2).Type() != domain {
		t.Fatal(pushed, err)
	}
	if filled.ArrayLen() != 2 || filled.ArrayElement(0).array != physical.array {
		t.Fatal("push mutated original domain array")
	}
	other := testTypes()
	_, err = ArrayCat(ctx, filled, Null(other[1007]))
	if err == nil {
		t.Fatal("foreign generation accepted on NULL fast path")
	}
	plan, err := BindArraySort(array, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := plan.Evaluate(ctx, pushed, true, false, InputEnvironment{})
	if err != nil || sorted.ArrayElement(0).Type() != domain || !sorted.ArrayElement(2).IsNull() {
		t.Fatal(sorted, err)
	}
	// Validation precedence is independent of a zero/negative/product fast path.
	bounds := arraySearchUnitInput(t, types[1007], "{1}")
	_, err = ArrayFill(ctx, types[1007], physical.ArrayElement(0), arraySearchUnitInput(t, types[1007], "{1,NULL}"), &bounds)
	arraySearchUnitState(t, err, "22004", "dimension values cannot be null")
}
