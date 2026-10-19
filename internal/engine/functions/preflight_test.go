package functions

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Preflight pins the fold each node names for statement preparation; nodes
// without the hook use PreflightSimplify.

func preflightOf(node expr.Expr) expr.Preflight {
	if hook, ok := node.(expr.Preflighting); ok {
		return hook.PreflightFold()
	}
	return expr.PreflightSimplify
}

func TestPreflightFolds(t *testing.T) {
	for _, c := range []struct {
		node expr.Expr
		want expr.Preflight
	}{
		{CurrentDateTime{}, expr.PreflightKeep},
		{TransactionTime{}, expr.PreflightKeep},
		{VersionInformation{}, expr.PreflightKeep},
		{RegexpSet{}, expr.PreflightChildren},
		{JSONEach{}, expr.PreflightChildren},
		{ArrayRandom{}, expr.PreflightChildren},
		{Random{}, expr.PreflightChildren},
		{Format{}, expr.PreflightChildren},
		{LikeEscape{}, expr.PreflightEvaluate},
		{Regexp{}, expr.PreflightEvaluate},
		{Posix{}, expr.PreflightRegexNullOrEvaluate},
		{SimilarEscape{}, expr.PreflightRegexNullOrEvaluate},
		{PosixSubstring{}, expr.PreflightRegexNullOrEvaluate},
		{Similar{}, expr.PreflightNullOrEvaluate},
		{SimilarSubstring{}, expr.PreflightNullOrEvaluate},
		{Timezone{}, expr.PreflightNullFirst},
		{DatetimeZoneCalendar{}, expr.PreflightNullFirst},
		{DatetimeSleep{}, expr.PreflightNullBare},
		{Binary{}, expr.PreflightSimplify},
		{String{}, expr.PreflightSimplify},
		{ArrayTransform{}, expr.PreflightSimplify},
		{DatetimeFormatting{}, expr.PreflightSimplify},
	} {
		if got := preflightOf(c.node); got != c.want {
			t.Errorf("%T: preflight %d, want %d", c.node, got, c.want)
		}
	}
}

func TestPreflightTemplateNeedsLiteral(t *testing.T) {
	never := func(expr.Expr) (sqltype.Value, bool) { return sqltype.Value{}, false }
	for _, node := range []expr.ConstantTemplate{DatetimeFormatting{}, NumericFormatting{}} {
		_, changed, err := node.SpecializeTemplate(context.Background(), never)
		if err != nil || changed {
			t.Errorf("%T specialized a non-literal template: %v %v", node, changed, err)
		}
	}
}

func TestArrayTransformEvaluatesFormalSlots(t *testing.T) {
	// Written as (element => e, array => a): the array formal comes first.
	node := ArrayTransform{Args: []expr.Expr{nil, nil}, Slots: [3]uint8{1, 0}}
	if got := node.EvaluationOrder(); len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Errorf("evaluation order %v, want [1 0]", got)
	}
}

func TestSubstituteKeepsTheProvider(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	plan, err := sqltype.BindBinary("btrim", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arg := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	node := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{arg, arg}}
	// A child whose usage the provider would not bind: Rebuild rejects it,
	// Substitute keeps the bound provider as preflight always did.
	other := binaryUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}}
	if _, err := node.Rebuild(node.Info, []expr.Expr{arg, other}); err == nil {
		t.Fatal("Rebuild bound a mismatched child")
	}
	got, err := node.Substitute(node.Info, []expr.Expr{arg, other})
	if err != nil || got.(Binary).Plan != plan || got.(Binary).Args[1] != other {
		t.Fatalf("Substitute: %v", err)
	}
	if _, err := node.Substitute(node.Info, []expr.Expr{arg}); err == nil {
		t.Fatal("Substitute accepted a different arity")
	}
}
