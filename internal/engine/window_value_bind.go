package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// A sort-input boundary retains exact required targets before matching window
// leaves from postponed volatile payloads. Only an ACTUAL whole retained target
// can own a leaf; unmatched postponed leaves keep independent execution slots.
func (b *expressionBinder) windowValueAliases(calls []plan.WindowCall, projection []expr.Expr, order, on []plan.SortKey) []int {
	if len(order) == 0 {
		return nil
	}
	hasValues := false
	for _, call := range calls {
		hasValues = hasValues || call.Operation.EvaluatesArgument()
	}
	if !hasValues {
		return nil
	}
	demanded := func(output int) bool {
		for _, keys := range [2][]plan.SortKey{order, on} {
			for _, key := range keys {
				if key.OutputOrdinal == output+1 || key.TargetIdentity == output+1 {
					return true
				}
			}
		}
		return false
	}
	owners := make([]*plan.WindowExpr, 0, len(calls))
	retain := func(expression expr.Expr) {
		leaf, ok := expression.(*plan.WindowExpr)
		if !ok || leaf.Frame != b.FrameID() || leaf.Call < 0 || leaf.Call >= len(calls) || !calls[leaf.Call].Operation.EvaluatesArgument() {
			return
		}
		for _, owner := range owners {
			if owner.Call == leaf.Call {
				return
			}
		}
		owners = append(owners, leaf)
	}
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != expr.Volatile {
			retain(expression)
		}
	}
	for _, keys := range [2][]plan.SortKey{order, on} {
		for _, key := range keys {
			retain(key.Expression)
		}
	}
	var aliases []int
	for i, expression := range projection {
		if demanded(i) || expression.Properties().Volatility != expr.Volatile {
			continue
		}
		plan.WalkExpression(expression, func(child expr.Expr) {
			leaf, ok := child.(*plan.WindowExpr)
			if !ok || leaf.Frame != b.FrameID() || leaf.Call < 0 || leaf.Call >= len(calls) || !calls[leaf.Call].Operation.EvaluatesArgument() {
				return
			}
			for _, owner := range owners {
				if !b.sameWindowCall(calls[leaf.Call], calls[owner.Call]) {
					continue
				}
				if leaf.Call != owner.Call {
					if aliases == nil {
						aliases = make([]int, len(calls))
						for j := range aliases {
							aliases[j] = -1
						}
					}
					aliases[leaf.Call] = owner.Call
				}
				return
			}
		})
	}
	return aliases
}
