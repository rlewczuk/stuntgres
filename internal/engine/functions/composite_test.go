package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// rebuildIdentity checks that rebuilding a protocol node from its own metadata
// and children reproduces it, and that a wrong child count is rejected.
func rebuildIdentity(t *testing.T, node expr.Composite, want []expr.Expr) {
	t.Helper()
	children := node.Children()
	if !reflect.DeepEqual(children, want) {
		t.Fatalf("%T children = %v, want %v", node, children, want)
	}
	got, err := node.Rebuild(expr.InfoOf(node), append([]expr.Expr(nil), children...))
	if err != nil || !reflect.DeepEqual(got, node) {
		t.Fatalf("%T rebuild = %#v, %v; want %#v", node, got, err, node)
	}
	if _, err := node.Rebuild(expr.InfoOf(node), append(children, nil)); err == nil {
		t.Fatalf("%T rebuild accepted an extra child", node)
	}
}

func TestCompositeSliceArguments(t *testing.T) {
	types := inspectCarrierTypes()
	d := types[sqltype.Int4Range]
	arg := func(index int) expr.Expr {
		return stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: sqltype.Null(d), index: index}
	}
	node, err := NewRangeInspect(expr.Info{Usage: d.Usage()}, sqltype.RangeMerge, []expr.Expr{arg(0), arg(1)})
	if err != nil {
		t.Fatal(err)
	}
	rebuildIdentity(t, node, []expr.Expr{arg(0), arg(1)})
	mapped, changed, err := expr.Map(node, func(child expr.Expr) (expr.Expr, bool, error) {
		if child.(stringUnitArgument).index == 1 {
			return arg(2), true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if got := mapped.(RangeInspect).Args; !reflect.DeepEqual(got, []expr.Expr{arg(0), arg(2)}) || node.Args[1] != arg(1) {
		t.Fatalf("mapped children %v, source %v", got, node.Args)
	}
}

func TestCompositeArityLimitedArguments(t *testing.T) {
	for _, kind := range []sqltype.NetworkFormatKind{sqltype.NetworkHost, sqltype.NetworkSameFamily} {
		node, _, _ := networkFormatUnit(t, kind, sqltype.Inet)
		rebuildIdentity(t, node, node.Args[:kind.Arity()])
	}
}

func TestCompositeCountedArgumentsKeepTrailingSlots(t *testing.T) {
	d := temporalTestTypes()
	plan, err := sqltype.BindDatetimeMake("make_date", d[sqltype.Date], 3)
	if err != nil {
		t.Fatal(err)
	}
	node := DatetimeMake{Info: expr.Info{Usage: d[sqltype.Date].Usage()}, Plan: plan, Count: 3}
	for i := range node.Args {
		v, err := sqltype.Integer(d[sqltype.Int4], int64(i))
		if err != nil {
			t.Fatal(err)
		}
		node.Args[i] = temporalTestLiteral(v)
	}
	rebuildIdentity(t, node, node.Args[:3])
}

func TestCompositeNamedArguments(t *testing.T) {
	text := inspectCarrierTypes()[sqltype.Text]
	pattern := stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), index: 0}
	escape := stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), index: 1}
	node, err := NewLikeEscape(expr.Info{Usage: text.Usage()}, pattern, escape)
	if err != nil {
		t.Fatal(err)
	}
	rebuildIdentity(t, node, []expr.Expr{pattern, escape})
}
