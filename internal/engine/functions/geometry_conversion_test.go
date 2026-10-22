package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func conversionProviderUnit(t *testing.T, op sqltype.GeometryConversion) (GeometryConversion, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i := range op.Arity() {
		d := types[op.Formal(i)]
		text := "(0,0)"
		switch d.ID() {
		case sqltype.Box:
			text = "(6,8),(0,0)"
		case sqltype.Circle:
			text = "<(0,0),2>"
		case sqltype.Polygon:
			text = "((0,0),(1,3),(2,0))"
		case sqltype.Float8:
			text = "-1"
		case sqltype.Point:
			if i == 1 {
				text = "(1,1)"
			}
		}
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: v, order: &order, index: i}
	}
	n, err := NewGeometryConversion(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}

func TestGeometryConversionProviderLifecycle(t *testing.T) {
	for op := sqltype.BoxCircle; op <= sqltype.LinePoints; op++ {
		n, c, order := conversionProviderUnit(t, op)
		fresh, _, _ := conversionProviderUnit(t, op)
		if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Immutable || !n.LooksLikeFunction() {
			t.Fatal("provider contract", op)
		}
		if _, err := n.Eval(c); err != nil {
			t.Fatal(err)
		}
		wantOrder := []int{0}
		if op.Arity() == 2 {
			wantOrder = append(wantOrder, 1)
		}
		if !reflect.DeepEqual(*order, wantOrder) {
			t.Fatal("evaluation order", *order)
		}
		rebuilt, err := n.Rebuild(fresh.Info, fresh.Children())
		if err != nil || rebuilt.(GeometryConversion).Validate() != nil || rebuilt.(GeometryConversion).Plan == n.Plan {
			t.Fatal("rebuild", err)
		}
		if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
			t.Fatal("foreign descriptor admitted")
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing operands admitted")
		}
		spec, err := n.Render()
		if err != nil || spec.Form != expr.FormCall || spec.Name != op.Name() || spec.Namespace != "pg_catalog" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
			t.Fatal("render", spec, err)
		}
		captured, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || captured.Count != op.Arity() {
			t.Fatal("capture", captured, err)
		}
		if _, ok, _ := n.Capture(expr.CaptureOperator); ok {
			t.Fatal("operator capture")
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
			t.Fatal("group identity")
		}
		if _, err := n.Substitute(n.Info, n.Children()); err != nil {
			t.Fatal(err)
		}
		left := n.Args[0].(stringUnitArgument)
		left.value = sqltype.Null(left.Usage.Type)
		n.Args[0] = left
		if op.Arity() == 2 {
			right := n.Args[1].(stringUnitArgument)
			failure := errors.New("conversion child error")
			right.err = failure
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, failure) {
				t.Fatal("runtime NULL hid child error", err)
			}
		}
		left.literal = true
		n.Args[0] = left
		*order = nil
		value, err := n.Eval(c)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type || len(*order) != 0 {
			t.Fatal("literal NULL", value, err)
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = cancelled
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation", err)
		}
	}
}

func TestGeometryConversionReplayShapes(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want bool
	}{
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", true},
		{"circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point)", false},
		{"pg_catalog.circle('(0,0)'::point,'-1'::float8)", false},
		{"pg_catalog.circle($1::pg_catalog.point,'-1'::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,(random())::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.text)", false},
		{"pg_catalog.circle(DISTINCT '(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", false},
	} {
		statements, err := parser.Parse("SELECT " + tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		call, ok := statements[0].(*parser.Select).Targets[0].(parser.FunctionExpr)
		if !ok {
			t.Fatal("not function syntax", tc.sql)
		}
		if got := CircleConstructorReplay(call); got != tc.want {
			t.Fatalf("%s: %t want %t", tc.sql, got, tc.want)
		}
	}
}
