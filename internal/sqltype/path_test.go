package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// Public IO/error/transport cases live in reference/geometry_network. These units target
// immutable ownership, bit identity and scalar-versus-generic authority.
func TestPathDatum(t *testing.T) {
	d := testTypes()[Path]
	a := boxUnitInput(t, d, "[(-0,2),(3,4),(3,4)]")
	for _, s := range []string{"[(0,2),(3,4),(3,4)]", "((-0,2),(3,4),(3,4))", "[(3,4),(-0,2),(3,4)]", "[(-0,2),(3,4)]"} {
		if SameDatum(a, boxUnitInput(t, d, s)) {
			t.Fatal("distinct path representations compared identical", s)
		}
	}
	copy := a
	if copy.path != a.path || !SameDatum(a, copy) {
		t.Fatal("Value copy changed immutable payload")
	}
	reowned, err := a.Retag(testTypes()[Path])
	if err != nil || reowned.path != a.path {
		t.Fatal("Retag copied or lost immutable payload", err)
	}
	if math.Float64bits(a.path.points[0][0]) != math.Float64bits(math.Copysign(0, -1)) {
		t.Fatal("signed zero lost")
	}
	separate := boxUnitInput(t, d, "[(-0,2),(3,4),(3,4)]")
	if same, err := a.SamePayload(context.Background(), separate); err != nil || !same || !SameDatum(a, separate) {
		t.Fatal("independently parsed payload identity", same, err)
	}
	if _, err := (Value{typ: d}).Retag(d); err == nil {
		t.Fatal("missing payload admitted")
	}
	if _, err := (Value{typ: d, path: &pathPayload{}}).Retag(d); err == nil {
		t.Fatal("empty payload admitted")
	}
}

func TestPathComparison(t *testing.T) {
	types := testTypes()
	d := types[Path]
	a, b := boxUnitInput(t, d, "[1,2]"), boxUnitInput(t, d, "((9,8),(7,6))")
	ctx := context.Background()
	for i, op := range []ComparisonOperator{CompareEqual, CompareLess, CompareGreater, CompareLessEqual, CompareGreaterEqual} {
		p, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(ctx, a, b, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := v.Boolean()
		if err != nil || truth != []bool{false, true, false, true, false}[i] {
			t.Fatal(op, truth, err)
		}
		if p.EqualityFor(d.Usage()) {
			t.Fatal("scalar count equality granted hash identity")
		}
		v, err = p.Evaluate(ctx, Null(d), b, InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != types[Bool] {
			t.Fatal("strict NULL", v, err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Evaluate(cancelled, Null(d), b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation after NULL", err)
		}
		if _, err := p.Evaluate(ctx, boxUnitInput(t, testTypes()[Path], "[1,2]"), b, InputEnvironment{}); err == nil {
			t.Fatal("foreign descriptor admitted")
		}
	}
	if _, err := BindComparisonPredicate(CompareNotEqual, d, d, types[Bool], CollationRef{}, false); err == nil {
		t.Fatal("invented not-equal")
	}
}

func TestPathDescriptor(t *testing.T) {
	types := testTypes()
	d := types[Path]
	if !d.Concrete() || d.ID() != 602 || d.DatabaseName() != "PATH" || d.IsArray() || types[1019].Concrete() {
		t.Fatal("scalar/array admission")
	}
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("generic capabilities")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("ordering admitted")
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("grouping admitted")
	}
	if _, err := BindComparison(d, d, CollationRef{}, false); err == nil {
		t.Fatal("generic comparator admitted")
	}
}

func TestPathExactReplay(t *testing.T) {
	d := testTypes()[Path]
	ctx := context.Background()
	for _, s := range []string{"[(0.10000000000000002,-0)]", "((5e-324,-5e-324),(NaN,Infinity))", "[(-NaN,1)]", "((1,-NaN),(NaN,-Infinity))"} {
		v := boxUnitInput(t, d, s)
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if !SameDatum(v, boxUnitInput(t, d, strings.Trim(literal, "'"))) {
			t.Fatal("exact replay lost coordinate bits or closure", literal)
		}
	}
}
