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
	rebuildIdentity(t, Timezone{Info: info, Value: value, Zone: zone}, []expr.Expr{zone, value})
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

// Planning hooks: stage-owned and never-evaluated calls opt out of folding,
// strict-NULL timing is per node, and only stored-plan providers validate.
func TestPlanningHooks(t *testing.T) {
	fold := func(node expr.Expr) expr.Fold {
		if hook, ok := node.(expr.Folding); ok {
			return hook.PlanningFold()
		}
		return expr.FoldConstant
	}
	strict := func(node expr.Expr) expr.Strict {
		if hook, ok := node.(expr.StrictFolding); ok {
			return hook.StrictNull()
		}
		return expr.StrictNone
	}
	stored := func(node expr.Expr) bool { _, ok := node.(expr.StoredProvider); return ok }
	for _, tc := range []struct {
		node   expr.Expr
		fold   expr.Fold
		strict expr.Strict
		stored bool
	}{
		{RangeInspect{}, expr.FoldConstant, expr.StrictNone, true},
		{JSONKeys{}, expr.FoldNever, expr.StrictNone, false},
		{RegexpSet{}, expr.FoldNever, expr.StrictNone, false},
		{Regexp{}, expr.FoldConstant, expr.StrictNone, true},
		{VersionInformation{}, expr.FoldNever, expr.StrictNone, false},
		{JSONCreate{}, expr.FoldNever, expr.StrictNone, true},
		{DatetimeOverlaps{}, expr.FoldOverlapsRoute, expr.StrictNone, true},
		{StringVariadic{}, expr.FoldQuoteCapture, expr.StrictNone, true},
		{StringVariadic{Stable: true}, expr.FoldNever, expr.StrictNone, true},
		{Timezone{}, expr.FoldConstant, expr.StrictBefore, true},
		{Binary{}, expr.FoldConstant, expr.StrictAfter, true},
		{UUIDGeneration{}, expr.FoldNever, expr.StrictAfter, false},
		{DatetimeSleep{}, expr.FoldNever, expr.StrictAfterBare, false},
	} {
		if got := fold(tc.node); got != tc.fold {
			t.Errorf("%T fold = %v, want %v", tc.node, got, tc.fold)
		}
		if got := strict(tc.node); got != tc.strict {
			t.Errorf("%T strict = %v, want %v", tc.node, got, tc.strict)
		}
		if got := stored(tc.node); got != tc.stored {
			t.Errorf("%T stored provider = %v, want %v", tc.node, got, tc.stored)
		}
	}
}

// Retag reports a retained-descriptor change even when the engine saw none,
// and keeps the node when nothing changed.
func TestRetagReportsDerivedInputChanges(t *testing.T) {
	d := temporalTestTypes()
	text := temporalTestLiteral(sqltype.String(d[sqltype.Text], "%s"))
	node := Format{Info: expr.Info{Usage: d[sqltype.Text].Usage()}, Args: []expr.Expr{text}, Int4: d[sqltype.Int4]}
	identity := expr.Retag{Type: func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }}
	if _, changed, err := node.Retag(node.Info, node.Args, false, identity); err != nil || changed {
		t.Fatalf("identity retag changed=%v err=%v", changed, err)
	}
	retained := sqltype.Builtins()[0]
	moved := expr.Retag{Type: func(t *sqltype.Descriptor) *sqltype.Descriptor {
		if t == d[sqltype.Int4] {
			return retained
		}
		return t
	}}
	got, changed, err := node.Retag(node.Info, node.Args, false, moved)
	if err != nil || !changed || got.(Format).Int4 != retained || node.Int4 != d[sqltype.Int4] {
		t.Fatalf("retained retag %#v changed=%v err=%v", got, changed, err)
	}
}

// The quote-capture origin is opaque: rebuilds keep it and only the engine
// replaces it.
func TestStringVariadicAnnotationRoundTrip(t *testing.T) {
	type origin struct{ owner int }
	node := StringVariadic{Origin: origin{1}}
	next := node.WithAnnotation(origin{2}).(StringVariadic)
	if next.Annotation() != (origin{2}) || node.Annotation() != (origin{1}) {
		t.Fatalf("annotation %v, source %v", next.Annotation(), node.Annotation())
	}
}
