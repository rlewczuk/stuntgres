package functions

import (
	"testing"

	"stuntgres/internal/engine/expr"
)

// FunctionSyntax pins which nodes pg_get_indexdef prints bare.

func looksLikeFunction(node expr.Expr) bool {
	hook, ok := node.(expr.FunctionSyntax)
	return ok && hook.LooksLikeFunction()
}

func TestFunctionSyntaxFollowsSpelling(t *testing.T) {
	for _, c := range []struct {
		node expr.Expr
		want bool
	}{
		{Timezone{Function: true}, true},
		{Timezone{}, false}, // AT TIME ZONE
		{CurrentDateTime{}, true},
		{DatetimeExtract{}, true},
		{RangeOperator{}, false},
		{ArrayRandom{}, false},
		{LikeEscape{}, false},
		{Posix{}, false},
		{JSONEach{}, false},
	} {
		if got := looksLikeFunction(c.node); got != c.want {
			t.Errorf("%T%+v: looks like a function %v, want %v", c.node, c.node, got, c.want)
		}
	}
}

func TestClosureTransparencyExcludesGenerators(t *testing.T) {
	for _, node := range []expr.Expr{String{}, Binary{}, Timezone{}, UUIDExtract{}, JSONCreate{}} {
		if _, ok := node.(expr.ClosureTransparent); !ok {
			t.Errorf("%T is not looked through", node)
		}
	}
	// Generators and the calls only one check looks through stay explicit.
	for _, node := range []expr.Expr{UUIDGeneration{}, TransactionTime{}, DatetimeSleep{}, Random{}, DatetimeTrunc{}, DatetimeBin{}, DatetimeZoneCalendar{}, JSONEach{}} {
		if _, ok := node.(expr.ClosureTransparent); ok {
			t.Errorf("%T is looked through", node)
		}
	}
}
