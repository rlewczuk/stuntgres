package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Nodes whose binding derives inputs from the engine keep those inputs when
// rebuilt; only retag replaces them.

func TestCompositeTimezoneChildrenFollowMode(t *testing.T) {
	d := temporalTestTypes()
	value := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 0))
	zone := temporalTestLiteral(sqltype.String(d[sqltype.Text], "UTC"))
	info := expr.Info{Usage: d[sqltype.Timestamp].Usage()}
	rebuildIdentity(t, Timezone{Info: info, Value: value, Zone: zone}, []expr.Expr{value, zone})
	local := Timezone{Info: info, Value: value, Local: true}
	rebuildIdentity(t, local, []expr.Expr{value})
}

func TestCompositeRetainedPlanKeepsTrailingSlots(t *testing.T) {
	d := temporalTestTypes()
	plan, err := sqltype.BindDatetimeTrunc(d[sqltype.Text], d[sqltype.Timestamptz], d[sqltype.Timestamptz], d[sqltype.Text])
	if err != nil {
		t.Fatal(err)
	}
	field := temporalTestLiteral(sqltype.String(d[sqltype.Text], "day"))
	source := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 0))
	zone := temporalTestLiteral(sqltype.String(d[sqltype.Text], "UTC"))
	node := DatetimeTrunc{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage()}, Plan: plan, Count: 3, Args: [3]expr.Expr{field, source, zone}}
	rebuildIdentity(t, node, []expr.Expr{field, source, zone})
	later := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 1))
	mapped, changed, err := expr.Map(node, func(child expr.Expr) (expr.Expr, bool, error) {
		if child == source {
			return later, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	got := mapped.(DatetimeTrunc)
	if got.Args != [3]expr.Expr{field, later, zone} || got.Plan != plan || node.Args[1] != source {
		t.Fatalf("mapped %#v, source %#v", got, node)
	}
}

func TestCompositeStringKeepsTextUsage(t *testing.T) {
	text := inspectCarrierTypes()[sqltype.Text]
	arg := func(index int) expr.Expr {
		return stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), index: index}
	}
	node, err := NewString(expr.Info{Usage: text.Usage()}, "||", text.Usage(), []expr.Expr{arg(0), arg(1)}, [4]bool{true, true}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	rebuildIdentity(t, node, []expr.Expr{arg(0), arg(1)})
	if _, err := node.Rebuild(node.Info, []expr.Expr{arg(0), nil}); err == nil || err.Error() != "stuntgres: invalid remapped string argument" {
		t.Fatalf("missing child accepted: %v", err)
	}
}

func TestCompositeLeafRebuildTakesMetadata(t *testing.T) {
	d := temporalTestTypes()
	node := TransactionTime{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage()}, Kind: 1}
	rebuildIdentity(t, node, nil)
	next := expr.Info{Usage: d[sqltype.Timestamptz].Usage(), Pos: node.Pos}
	next.Pos.Offset = 7
	got, err := node.Rebuild(next, nil)
	if err != nil || got.(TransactionTime).Info != next || got.(TransactionTime).Kind != node.Kind {
		t.Fatal(got, err)
	}
}

func TestCompositeProviderlessRebuildOwnsChildren(t *testing.T) {
	d := temporalTestTypes()
	a := temporalTestLiteral(sqltype.String(d[sqltype.Text], "a"))
	b := temporalTestLiteral(sqltype.String(d[sqltype.Text], "b"))
	node := NullCount{Info: expr.Info{Usage: d[sqltype.Int4].Usage()}, Kind: CountNulls, Args: []expr.Expr{a, a}}
	rebuildIdentity(t, node, []expr.Expr{a, a})
	children := []expr.Expr{a, b}
	got, err := node.Rebuild(node.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	children[1] = a
	if args := got.(NullCount).Args; !reflect.DeepEqual(args, []expr.Expr{a, b}) {
		t.Fatalf("rebuilt node aliases its input: %v", args)
	}
}

// IntrinsicVolatility excludes children: fixed and plan-selected values, and
// the analyzed volatility for nodes that cannot retain eliminated arms.
func TestIntrinsicVolatilityExcludesChildren(t *testing.T) {
	for _, tc := range []struct {
		node expr.Intrinsic
		want expr.Volatility
	}{
		{RangeInspect{Info: expr.Info{Property: expr.Properties{Volatility: expr.Volatile}}}, expr.Immutable},
		{JSONStripNulls{}, expr.Stable},
		{ArrayRandom{}, expr.Volatile},
		{Timezone{Local: true}, expr.Stable},
		{TransactionTime{Info: expr.Info{Property: expr.Properties{Volatility: expr.Stable}}}, expr.Stable},
	} {
		if got := tc.node.IntrinsicVolatility(); got != tc.want {
			t.Errorf("%T intrinsic volatility = %v, want %v", tc.node, got, tc.want)
		}
	}
}
