package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func networkMaskUnit(t *testing.T, k sqltype.NetworkFormatKind, input sqltype.ID) (NetworkFormat, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i := range k.Arity() {
		typ, value := types[input], "192.168.1.0/24"
		if k == sqltype.NetworkSetMasklen && i == 1 {
			typ, value = types[sqltype.Int4], "16"
		}
		v, err := typ.Input(context.Background(), value, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: typ.Usage()}, value: v, order: &order, index: i}
	}
	node, err := NewNetworkFormat(expr.Info{Usage: types[k.ResultFor(input)].Usage()}, k, args)
	if err != nil {
		t.Fatal(err)
	}
	return node, &stringUnitContext{Context: context.Background()}, &order
}

func TestNetworkMaskReconstruction(t *testing.T) {
	for _, tc := range []struct {
		k     sqltype.NetworkFormatKind
		input sqltype.ID
	}{
		{sqltype.NetworkHostmask, sqltype.Inet}, {sqltype.NetworkNetmask, sqltype.Inet}, {sqltype.NetworkNetwork, sqltype.Inet},
		{sqltype.NetworkMerge, sqltype.Inet}, {sqltype.NetworkSetMasklen, sqltype.Inet}, {sqltype.NetworkSetMasklen, sqltype.Cidr},
	} {
		original, c, _ := networkMaskUnit(t, tc.k, tc.input)
		fresh, _, _ := networkMaskUnit(t, tc.k, tc.input)
		if original.Validate() != nil {
			t.Fatal("invalid original")
		}
		rebuilt, err := original.Rebuild(fresh.Info, fresh.Children())
		if err != nil {
			t.Fatal(err)
		}
		remapped := rebuilt.(NetworkFormat)
		if remapped.Validate() != nil || remapped.Plan == original.Plan {
			t.Fatal("stale plan")
		}
		if remapped.Args[0].ResultUsage().Type != fresh.Args[0].ResultUsage().Type || remapped.ResultUsage().Type != fresh.ResultUsage().Type {
			t.Fatal("foreign descriptors retained")
		}
		value, err := remapped.Eval(c)
		if err != nil || value.Type() != fresh.Usage.Type {
			t.Fatal(value, err)
		}
		substituted, err := original.Substitute(fresh.Info, fresh.Children())
		if err != nil || substituted.(NetworkFormat).Validate() != nil {
			t.Fatal("substitution", err)
		}
		if _, err := original.Reconstruct(original.Info, fresh.Args); err == nil {
			t.Fatal("foreign result accepted")
		}
		rendering, err := remapped.Render()
		if err != nil || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
			t.Fatal("symbolic render", err)
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !original.SameGroup(original, same, false) || original.SameGroup(fresh, same, false) {
			t.Fatal("plan group identity")
		}
		bad := original
		bad.Kind = 0
		if bad.Validate() == nil {
			t.Fatal("bad kind")
		}
		bad = original
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing child")
		}
		if tc.k == sqltype.NetworkSetMasklen {
			bad = original
			bad.Args[1] = bad.Args[0]
			if bad.Validate() == nil {
				t.Fatal("network accepted as mask integer")
			}
			types := inspectCarrierTypes()
			bad = original
			wrong := sqltype.Cidr
			if tc.input == sqltype.Cidr {
				wrong = sqltype.Inet
			}
			bad.Usage = types[wrong].Usage()
			if bad.Validate() == nil {
				t.Fatal("setter overload result changed")
			}
		}
		if _, err := original.Rebuild(original.Info, nil); err == nil {
			t.Fatal("missing rebuild arguments")
		}
		if _, err := original.Substitute(original.Info, nil); err == nil {
			t.Fatal("missing substitute arguments")
		}
		if original.Validate() != nil {
			t.Fatal("mutated source")
		}
	}
}

func TestNetworkMaskStrictDemand(t *testing.T) {
	for _, tc := range []struct {
		k     sqltype.NetworkFormatKind
		input sqltype.ID
	}{
		{sqltype.NetworkHostmask, sqltype.Inet}, {sqltype.NetworkNetmask, sqltype.Inet}, {sqltype.NetworkNetwork, sqltype.Inet},
		{sqltype.NetworkMerge, sqltype.Inet}, {sqltype.NetworkSetMasklen, sqltype.Inet}, {sqltype.NetworkSetMasklen, sqltype.Cidr},
	} {
		for nullAt := range tc.k.Arity() {
			node, c, order := networkMaskUnit(t, tc.k, tc.input)
			arg := node.Args[nullAt].(stringUnitArgument)
			arg.value = sqltype.Null(arg.Usage.Type)
			node.Args[nullAt] = arg
			v, err := node.Eval(c)
			wantOrder := []int{0}
			if tc.k.Arity() == 2 {
				wantOrder = append(wantOrder, 1)
			}
			if err != nil || !v.IsNull() || v.Type() != node.Usage.Type || !reflect.DeepEqual(*order, wantOrder) {
				t.Fatal(tc, v, err, *order)
			}
			if tc.k.Arity() == 2 {
				sibling := 1 - nullAt
				other := node.Args[sibling].(stringUnitArgument)
				failure := errors.New("mask child failure")
				other.err = failure
				node.Args[sibling] = other
				*order = nil
				if _, err := node.Eval(c); !errors.Is(err, failure) {
					t.Fatal("runtime null suppressed child failure", err)
				}
			}
			arg.literal = true
			node.Args[nullAt] = arg
			*order = nil
			v, err = node.Eval(c)
			if err != nil || !v.IsNull() || len(*order) != 0 {
				t.Fatal("literal null demand", v, err, *order)
			}
		}
	}
}

func TestNetworkMaskCancellation(t *testing.T) {
	for _, k := range []sqltype.NetworkFormatKind{sqltype.NetworkHostmask, sqltype.NetworkNetmask, sqltype.NetworkNetwork, sqltype.NetworkMerge, sqltype.NetworkSetMasklen} {
		inputs := []sqltype.ID{sqltype.Inet}
		if k == sqltype.NetworkSetMasklen {
			inputs = append(inputs, sqltype.Cidr)
		}
		for _, input := range inputs {
			n, c, order := networkMaskUnit(t, k, input)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) || len(*order) != 0 {
				t.Fatal(k, err, *order)
			}
		}
	}
}
