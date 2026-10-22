package sqltype

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestLineInputOutput(t *testing.T) {
	d := testTypes()[Line]
	ctx := context.Background()
	for _, tc := range [][2]string{{"{2,4,6}", "{2,4,6}"}, {"[(1,2),(3,4)]", "{1,-1,1}"}, {"(1,3),(2,3)", "{0,-1,3}"}, {"(3,1),(3,2)", "{-1,0,3}"}, {"(0,0),(6,6)", "{1,-1,0}"}, {"{1,-0,-0}", "{1,-0,-0}"}, {"{3,NaN,5}", "{3,NaN,5}"}} {
		v := boxUnitInput(t, d, tc[0])
		out, err := d.Output(ctx, v)
		if err != nil || out != tc[1] {
			t.Fatal(tc, out, err)
		}
		if math.Float64bits(v.geometry.coordinates[3]) != 0 {
			t.Fatal("unused coordinate is not positive zero")
		}
	}
}

func TestLineScanner(t *testing.T) {
	d := testTypes()[Line]
	for _, s := range []string{"(1,2)(3,4)", "1,2,3,4,", "(1,2,3,4)", "((1,2),(3,4))", "[(1,2),(3,4))"} {
		boxUnitInput(t, d, s)
	}
	for _, tc := range []struct{ s, code, message string }{
		{"[(1,2,3,4]", "22P02", "invalid input syntax"}, {"[ ((1,2),(3,4)]", "22P02", "invalid input syntax"},
		{"{0,0,1} x", "22P02", "invalid input syntax"}, {"{0.000001,-0.000001,3}", "22P02", "A and B cannot both be zero"}, {"(0,0),(0.000001,0.000001)", "22P02", "must be two distinct points"}, {"(NaN,2),(NaN,2)", "22P02", "must be two distinct points"},
		{"{1,2,1e-4000}", "22003", "out of range"}, {"(1e308,1e308),(-1e308,-1e308)", "22003", "overflow"}, {"(1e-308,0),(1e308,1)", "22003", "underflow"},
	} {
		_, err := d.Input(context.Background(), tc.s, -1)
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != tc.code || !strings.Contains(err.Error(), tc.message) {
			t.Fatal(tc, err)
		}
	}
	boxUnitInput(t, d, "{0.0000010001,0,3}")
}

func TestLineComparison(t *testing.T) {
	types := testTypes()
	d := types[Line]
	ctx := context.Background()
	p, err := BindComparisonPredicate(CompareEqual, d, d, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"{1,2,3}", "{2,4,6}", true}, {"{1,2,3}", "{2,4,7}", false}, {"{NaN,2,3}", "{NaN,2,3}", true}, {"{NaN,2,3}", "{NaN,4,6}", false}, {"{1,2,3}", "{1.0000005,2,3}", false}, {"{1,2,3}", "{1,2.0000005,3}", true}} {
		v, err := p.Evaluate(ctx, boxUnitInput(t, d, tc.a), boxUnitInput(t, d, tc.b), InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := v.Boolean()
		if err != nil || truth != tc.want {
			t.Fatal(tc, truth, err)
		}
	}
	v, err := p.Evaluate(ctx, Null(d), boxUnitInput(t, d, "{1,2,3}"), InputEnvironment{})
	if err != nil || !v.IsNull() || v.Type() != types[Bool] {
		t.Fatal(v, err)
	}
	for _, op := range []ComparisonOperator{CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual} {
		if _, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false); err == nil {
			t.Fatal("invented LINE comparison", op)
		}
	}
	for _, tc := range []struct{ a, b, kind string }{
		{"{1e308,1,0}", "{0.00001,1,0}", "overflow"},
		{"{1e-308,1,0}", "{1e308,1,0}", "underflow"},
	} {
		_, err := p.Evaluate(ctx, boxUnitInput(t, d, tc.a), boxUnitInput(t, d, tc.b), InputEnvironment{})
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != "22003" || !strings.Contains(err.Error(), tc.kind) {
			t.Fatal(tc, err)
		}
	}
}

func TestLineDatum(t *testing.T) {
	d := testTypes()[Line]
	ctx := context.Background()
	for _, tc := range [][2]string{{"{1,2,3}", "{2,4,6}"}, {"{1,0,0}", "{1,-0,-0}"}} {
		a, b := boxUnitInput(t, d, tc[0]), boxUnitInput(t, d, tc[1])
		if SameDatum(a, b) {
			t.Fatal("SQL equality leaked into datum identity")
		}
		if same, err := a.SamePayload(ctx, b); err != nil || same {
			t.Fatal("payload identity", same, err)
		}
	}
	a := boxUnitInput(t, d, "{1,-0,-0}")
	b, err := a.Retag(testTypes()[Line])
	if err != nil || b.geometry != a.geometry || !b.sameGeometry(a) {
		t.Fatal("retag changed immutable bits", err)
	}
}

func TestLineDescriptor(t *testing.T) {
	d := testTypes()[Line]
	if !d.Concrete() || d.ID() != 628 || d.DatabaseName() != "LINE" {
		t.Fatal("LINE descriptor", d)
	}
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("LINE acquired default comparison")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("LINE ordering")
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("LINE grouping")
	}
}

func TestLineExactReplay(t *testing.T) {
	d := testTypes()[Line]
	ctx := context.Background()
	for _, s := range []string{"{1,2,0.10000000000000002}", "{1,2,5e-324}", "{1,-0,-0}", "{Infinity,1,NaN}"} {
		v := boxUnitInput(t, d, s)
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		replay := boxUnitInput(t, d, strings.Trim(literal, "'"))
		if !SameDatum(v, replay) {
			t.Fatal("replay lost bits", literal)
		}
	}
}
