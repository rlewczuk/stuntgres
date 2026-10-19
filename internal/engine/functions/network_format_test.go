package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func networkFormatUnit(t *testing.T, kind sqltype.NetworkFormatKind, input sqltype.ID) (NetworkFormat, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	v, err := types[input].Input(context.Background(), "192.168.1.0/24", -1)
	if err != nil {
		t.Fatal(err)
	}
	var args [2]expr.Expr
	for i := range kind.Arity() {
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[input].Usage()}, value: v, order: &order, index: i}
	}
	node, err := NewNetworkFormat(expr.Info{Usage: types[kind.ResultFor(input)].Usage()}, kind, args)
	if err != nil {
		t.Fatal(err)
	}
	return node, &stringUnitContext{Context: context.Background()}, &order
}
func TestNetworkFormatReconstruction(t *testing.T) {
	for _, kind := range []sqltype.NetworkFormatKind{sqltype.NetworkAbbrev, sqltype.NetworkHost, sqltype.NetworkFamily, sqltype.NetworkMasklen, sqltype.NetworkSameFamily, sqltype.NetworkBroadcast} {
		original, c, _ := networkFormatUnit(t, kind, sqltype.Inet)
		if original.Validate() != nil || NetworkFormatIntrinsicRule() != expr.ExactImmutable {
			t.Fatal("invalid intrinsic carrier")
		}
		types := inspectCarrierTypes()
		var args [2]expr.Expr
		order := []int{}
		for i := range kind.Arity() {
			v, err := types[sqltype.Inet].Input(context.Background(), "::1", -1)
			if err != nil {
				t.Fatal(err)
			}
			args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Inet].Usage()}, value: v, order: &order, index: i}
		}
		remapped, err := original.Reconstruct(expr.Info{Usage: types[kind.ResultFor(sqltype.Inet)].Usage()}, args)
		if err != nil || remapped.Validate() != nil {
			t.Fatal(err)
		}
		if remapped.Plan == original.Plan || original.Args[0].ResultUsage().Type == args[0].ResultUsage().Type {
			t.Fatal("reconstruction retained foreign descriptors")
		}
		got, err := remapped.Eval(c)
		if err != nil || got.Type() != types[kind.ResultFor(sqltype.Inet)] {
			t.Fatal(got, err)
		}
		source, err := original.Eval(c)
		if err != nil || source.Type() != original.Usage.Type {
			t.Fatal("source mutated", source, err)
		}
		if _, err := remapped.Reconstruct(original.Info, args); kind == sqltype.NetworkBroadcast && err == nil {
			t.Fatal("foreign INET result accepted")
		}
		bad := original
		bad.Kind = 0
		if bad.Validate() == nil {
			t.Fatal("invalid operation")
		}
		bad = original
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing child")
		}
		bad = original
		bad.Usage = types[sqltype.Bool].Usage()
		if kind.ResultFor(sqltype.Inet) != sqltype.Bool && bad.Validate() == nil {
			t.Fatal("wrong result")
		}
		if kind.Arity() == 1 {
			bad = original
			bad.Args[1] = args[0]
			if bad.Validate() == nil {
				t.Fatal("unused argument retained")
			}
		}
	}
	node, _, _ := networkFormatUnit(t, sqltype.NetworkAbbrev, sqltype.Cidr)
	if node.Validate() != nil {
		t.Fatal("CIDR overload")
	}
}
func TestNetworkFormatStrictDemand(t *testing.T) {
	node, c, order := networkFormatUnit(t, sqltype.NetworkSameFamily, sqltype.Inet)
	v, err := node.Eval(c)
	if err != nil || v.IsNull() || !reflect.DeepEqual(*order, []int{0, 1}) {
		t.Fatal(v, err, *order)
	}
	left := node.Args[0].(stringUnitArgument)
	left.value = sqltype.Null(left.Usage.Type)
	node.Args[0] = left
	*order = nil
	v, err = node.Eval(c)
	if err != nil || !v.IsNull() || v.Type() != node.Usage.Type || !reflect.DeepEqual(*order, []int{0, 1}) {
		t.Fatal(v, err, *order)
	}
	failure := errors.New("selected network child failure")
	right := node.Args[1].(stringUnitArgument)
	right.err = failure
	node.Args[1] = right
	*order = nil
	if _, err := node.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(*order, []int{0, 1}) {
		t.Fatal(err, *order)
	}
	left.literal = true
	node.Args[0] = left
	*order = nil
	v, err = node.Eval(c)
	if err != nil || !v.IsNull() || len(*order) != 0 {
		t.Fatal(v, err, *order)
	}
	// Runtime NULL is evaluated once; literal strict NULL is not evaluated.
	for _, kind := range []sqltype.NetworkFormatKind{sqltype.NetworkAbbrev, sqltype.NetworkHost, sqltype.NetworkFamily, sqltype.NetworkMasklen, sqltype.NetworkBroadcast} {
		n, c, order := networkFormatUnit(t, kind, sqltype.Inet)
		arg := n.Args[0].(stringUnitArgument)
		arg.value = sqltype.Null(arg.Usage.Type)
		n.Args[0] = arg
		got, err := n.Eval(c)
		if err != nil || !got.IsNull() || got.Type() != n.Usage.Type || len(*order) != 1 {
			t.Fatal(kind, got, err, *order)
		}
		arg.literal = true
		n.Args[0] = arg
		*order = nil
		got, err = n.Eval(c)
		if err != nil || !got.IsNull() || len(*order) != 0 {
			t.Fatal(kind, got, err, *order)
		}
	}
}
func TestNetworkFormatCancellation(t *testing.T) {
	for _, kind := range []sqltype.NetworkFormatKind{sqltype.NetworkAbbrev, sqltype.NetworkHost, sqltype.NetworkFamily, sqltype.NetworkMasklen, sqltype.NetworkSameFamily, sqltype.NetworkBroadcast} {
		n, c, order := networkFormatUnit(t, kind, sqltype.Inet)
		ctx, cancel := context.WithCancel(c.Context)
		cancel()
		c.Context = ctx
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) || len(*order) != 0 {
			t.Fatal(kind, err, *order)
		}
	}
}
