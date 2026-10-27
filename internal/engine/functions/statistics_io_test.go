package functions

import (
	"testing"

	"stuntgres/internal/engine/expr"
)

func TestStatisticsObjectIOVolatility(t *testing.T) {
	for _, operation := range []string{"in", "out", "send"} {
		node := StatisticsIO{Operation: operation}
		volatility, fold, preflight := expr.Immutable, expr.FoldConstant, expr.PreflightNullOrEvaluate
		if operation == "send" {
			volatility, fold, preflight = expr.Stable, expr.FoldNever, expr.PreflightChildren
		}
		if node.IntrinsicVolatility() != volatility || node.PlanningFold() != fold || node.PreflightFold() != preflight {
			t.Fatal("statistics I/O executable volatility protocol", operation)
		}
	}
}
