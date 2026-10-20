package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// NULL recipes need no runtime authority. These are actual production nodes,
// with observed children, not an echo implementation of the runtime port.
func TestAdvisoryNodeRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	one, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, lifetime := range []AdvisoryLifetime{AdvisorySession, AdvisoryTransaction} {
		name := "pg_try_advisory_lock"
		if lifetime == AdvisoryTransaction {
			name = "pg_try_advisory_xact_lock"
		}
		var order []int
		n := Advisory{Info: expr.Info{Usage: types[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: name, Mode: AdvisoryExclusive, Action: AdvisoryTry, Lifetime: lifetime, Args: []expr.Expr{
			formatUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, value: sqltype.Null(types[sqltype.Int4]), order: &order, index: 0},
			formatUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, value: one, order: &order, index: 1},
		}}
		value, err := n.Eval(&formatUnitContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != types[sqltype.Bool] || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal(value, order, err)
		}
		failure := errors.New("demanded advisory sibling")
		order = nil
		n.Args[1] = formatUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, order: &order, index: 1, err: failure}
		if _, err := n.Eval(&formatUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("runtime NULL suppressed sibling", order, err)
		}
	}
}

func TestAdvisoryNodeProtocols(t *testing.T) {
	types := formatUnitTypes()
	a := &captureMarker{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, index: 0}
	b := &captureMarker{Info: a.Info, index: 1}
	n := Advisory{Info: expr.Info{Usage: types[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "pg_try_advisory_xact_lock", Mode: AdvisoryExclusive, Action: AdvisoryTry, Lifetime: AdvisoryTransaction, Args: []expr.Expr{a, b}}
	if !n.ValidStored() {
		t.Fatal("valid recipe rejected")
	}
	unchanged, changed, err := expr.Map(n, func(x expr.Expr) (expr.Expr, bool, error) { return x, false, nil })
	if err != nil || changed || !reflect.DeepEqual(unchanged, n) {
		t.Fatal(unchanged, changed, err)
	}
	mapped, changed, err := expr.Map(n, func(x expr.Expr) (expr.Expr, bool, error) {
		if x == a {
			return b, true, nil
		}
		return x, false, nil
	})
	if err != nil || !changed || mapped.(Advisory).Args[0] != b || n.Args[0] != a {
		t.Fatal("copy-on-change", mapped, changed, err)
	}
	info := n.Info
	info.Usage = types[sqltype.Void].Usage()
	substituted, err := expr.Substitute(n, info, []expr.Expr{b, a})
	if err != nil || substituted.ResultUsage() != info.Usage || substituted.(Advisory).Lifetime != n.Lifetime || n.ResultUsage() != types[sqltype.Bool].Usage() || n.Args[0] != a {
		t.Fatal("mapped metadata/recipe", substituted, err)
	}
	operands, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Check(n, 2) != nil {
		t.Fatal(operands, ok, err)
	}
	for i, want := range []expr.Expr{a, b} {
		if got, err := operands.At(n, i); err != nil || got != want {
			t.Fatal(i, got, err)
		}
	}
	same := func(a, b expr.Expr) bool { return a == b }
	other := n
	other.Args = []expr.Expr{b, a}
	if !n.SameGroup(n, same, false) || n.SameGroup(other, same, false) {
		t.Fatal("group order")
	}
	r, err := n.Render()
	if err != nil || r.Tree != "AdvisoryTransaction pg_try_advisory_xact_lock" || r.Name != n.Name || r.Form != expr.FormCall || !r.Replay.RelationOnly || r.Replay.Validate() != nil || r.Args[0].Expr != a || r.Args[1].Expr != b {
		t.Fatal(r, err)
	}
	for _, damage := range []func(*Advisory){
		func(x *Advisory) { x.Name = "pg_advisory_lock" },
		func(x *Advisory) { x.Action = AdvisoryUnlock },
		func(x *Advisory) { x.Mode = AdvisoryShare },
		func(x *Advisory) { x.Lifetime = AdvisorySession },
		func(x *Advisory) { x.Args = []expr.Expr{a} },
		func(x *Advisory) { x.Usage = types[sqltype.Void].Usage() },
	} {
		bad := n
		damage(&bad)
		if bad.ValidStored() {
			t.Fatal("damaged stored recipe accepted", bad)
		}
		r, err := bad.Render()
		if err == nil && r.Replay.Validate() == nil {
			t.Fatal("damaged replay admitted")
		}
	}
}

func TestBlockingInquiryNodeProtocols(t *testing.T) {
	types := temporalTestTypes()
	integer := types[sqltype.Int4]
	array := types[integer.AssociatedArray()]
	a := &captureMarker{Info: expr.Info{Usage: integer.Usage()}, index: 0}
	b := &captureMarker{Info: a.Info, index: 1}
	n := BlockingInquiry{Info: expr.Info{Usage: array.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Kind: OrdinaryBlockingInquiry, Arg: a}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	mapped, changed, err := expr.Map(n, func(x expr.Expr) (expr.Expr, bool, error) { return b, true, nil })
	if err != nil || !changed || mapped.(BlockingInquiry).Arg != b || n.Arg != a {
		t.Fatal(mapped, changed, err)
	}
	unchanged, changed, err := expr.Map(n, func(x expr.Expr) (expr.Expr, bool, error) { return x, false, nil })
	if err != nil || changed || !reflect.DeepEqual(unchanged, n) {
		t.Fatal(unchanged, changed, err)
	}
	info := n.Info
	info.Property.StageDependent = true
	substituted, err := expr.Substitute(n, info, []expr.Expr{b})
	if err != nil || !reflect.DeepEqual(expr.InfoOf(substituted), info) || substituted.(BlockingInquiry).Arg != b || n.Arg != a || n.Properties().StageDependent {
		t.Fatal("substitution lost supplied stage metadata or mutated source", substituted, err)
	}
	rebuilt, err := n.Rebuild(info, []expr.Expr{b})
	wantProperty := b.Properties()
	wantProperty.Volatility = expr.Volatile
	if err != nil || rebuilt.Properties() != wantProperty || rebuilt.ResultUsage() != info.Usage || rebuilt.SourcePosition() != info.Pos {
		t.Fatal("reconstruction must refresh only child properties", rebuilt, err)
	}
	r, err := n.Render()
	if err != nil || r.Tree != "BlockingInquiry 0" || r.Name != "pg_blocking_pids" || r.Args[0].Expr != a || r.Inputs[0] != a {
		t.Fatal(r, err)
	}
	operands, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Check(n, 1) != nil {
		t.Fatal(operands, ok, err)
	}
	if got, err := operands.At(n, 0); err != nil || got != a {
		t.Fatal(got, err)
	}
	for _, damage := range []func(*BlockingInquiry){
		func(x *BlockingInquiry) { x.Kind = BlockingKind(99) },
		func(x *BlockingInquiry) { x.Arg = nil },
		func(x *BlockingInquiry) { x.Usage = integer.Usage() },
		func(x *BlockingInquiry) { x.Property.Volatility = expr.Stable },
	} {
		bad := n
		damage(&bad)
		if bad.Validate() == nil {
			t.Fatal("damaged recipe accepted", bad)
		}
	}
	var order []int
	n.Arg = formatUnitArgument{Info: a.Info, value: sqltype.Null(integer), order: &order}
	value, err := n.Eval(&formatUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || value.Type() != array || !reflect.DeepEqual(order, []int{0}) {
		t.Fatal("strict typed NULL", value, order, err)
	}
}
