package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestGeometryAlignmentPredicates(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, tc := range []struct {
		name        string
		left, right ID
		a, b        string
		want        bool
		overflow    bool
	}{
		{"?-", 0, Line, "{0,1,3}", "", true, false}, {"?|", 0, Line, "{1,0,3}", "", true, false},
		{"?-", 0, Line, "{0.001,1000,0}", "", false, false}, {"?|", 0, Line, "{1,1,0}", "", false, false},
		{"?-", 0, Lseg, "[(0,0),(0,0)]", "", true, false}, {"?|", 0, Lseg, "[(0,0),(0,0)]", "", true, false},
		{"?-", 0, Lseg, "[(0,0),(0.000002,0.000002)]", "", false, false},
		{"?||", Line, Line, "{1,-1,0}", "{2,-2,4}", true, false},
		{"?||", Line, Line, "{1,1,1e308}", "{1,-1,1e308}", false, true},
		{"?-|", Line, Line, "{1,-1,0}", "{1,1,7}", true, false},
		{"?-|", Line, Line, "{1e200,1,0}", "{1e200,1,0}", false, true},
		{"?||", Lseg, Lseg, "[(0,0),(0,0)]", "[(2,0),(2,4)]", true, false},
		{"?-|", Lseg, Lseg, "[(0,0),(0,0)]", "[(0,2),(4,2)]", true, false},
		{"?-|", Lseg, Lseg, "[(0,0),(0,0)]", "[(0,0),(0,0)]", false, false},
		{"?||", Lseg, Lseg, "[(1e308,1),(-1e308,2)]", "[(0,0),(1,1)]", false, true},
	} {
		p := relationUnitPlan(t, types, tc.name, tc.left, tc.right)
		op := p.Operation()
		left, right := op.Signature()
		if left != tc.left || right != tc.right || op.OperandID(-1) != Unknown || op.OperandID(op.Arity()) != Unknown {
			t.Fatal("formal identity", tc)
		}
		var inputs [2]Usage
		var values [2]Value
		for i := range op.Arity() {
			id := op.OperandID(i)
			inputs[i] = types[id].Usage()
			text := tc.a
			if i == 1 {
				text = tc.b
			}
			values[i] = circleUnitInput(t, types[id], text)
		}
		v, err := p.Evaluate(context.Background(), values)
		if tc.overflow {
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != "22003" {
				t.Fatal("expected overflow", tc, err)
			}
		} else if err != nil || v.Type() != types[Bool] || v.IsNull() || v.boolean != tc.want {
			t.Fatal("predicate", tc, v, err)
		}
		for i := range op.Arity() {
			bad := inputs
			bad[i] = foreign[op.OperandID(i)].Usage()
			if _, err := BindGeometryRelation(op, types[Bool].Usage(), bad); err == nil {
				t.Fatal("foreign input", tc, i)
			}
			bad = inputs
			bad[i].Typmod = 1
			if _, err := BindGeometryRelation(op, types[Bool].Usage(), bad); err == nil {
				t.Fatal("altered usage", tc, i)
			}
			badValues := values
			badValues[i] = Null(foreign[op.OperandID(i)])
			if _, err := p.Evaluate(context.Background(), badValues); err == nil {
				t.Fatal("foreign NULL", tc, i)
			}
			badValues = values
			badValues[i] = Value{typ: types[op.OperandID(i)]}
			if _, err := p.Evaluate(context.Background(), badValues); err == nil {
				t.Fatal("missing payload", tc, i)
			}
		}
		if _, err := BindGeometryRelation(op, foreign[Bool].Usage(), inputs); err == nil {
			t.Fatal("foreign BOOL", tc)
		}
		var nulls [2]Value
		for i := range op.Arity() {
			nulls[i] = Null(types[op.OperandID(i)])
		}
		v, err = p.Evaluate(context.Background(), nulls)
		if err != nil || !v.IsNull() || v.Type() != types[Bool] {
			t.Fatal("typed NULL", tc, v, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, nulls); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation", err)
		}
		if op.Arity() == 1 {
			inputs[1] = inputs[0]
			if _, err := BindGeometryRelation(op, types[Bool].Usage(), inputs); err == nil {
				t.Fatal("extra usage")
			}
			nulls[1] = nulls[0]
			if _, err := p.Evaluate(context.Background(), nulls); err == nil {
				t.Fatal("extra value")
			}
		}
	}
	// BOX NaN exactness is local to each raw corner, never a renormalization.
	a := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{math.NaN(), 1, 0, 0}}}
	for _, tc := range []struct {
		coords [4]float64
		want   bool
	}{
		{[4]float64{math.NaN(), 1, 0, 0}, true},
		{[4]float64{math.NaN(), 1.0000005, 0, 0}, false},
		{[4]float64{math.NaN(), 1, 0, 0.0000005}, true},
	} {
		b := Value{typ: types[Box], geometry: &geometryPayload{coordinates: tc.coords}}
		if got, err := boxSame(a, b); err != nil || got != tc.want {
			t.Fatal("corner sameness", tc, got, err)
		}
	}
	a.geometry = &geometryPayload{coordinates: [4]float64{-1, -1, 0, 0}}
	b := Value{typ: types[Box], geometry: &geometryPayload{coordinates: [4]float64{0, 0, -1, -1}}}
	if got, err := boxSame(a, b); err != nil || got {
		t.Fatal("raw inverted corners normalized", got, err)
	}
}
