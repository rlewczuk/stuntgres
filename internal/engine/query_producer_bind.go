package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func (b *expressionBinder) scheduleQueryProjection(root plan.Node, projection []expr.Expr, bounded bool) plan.Node {
	base := 0
	var lateSlots []int
	ordered, sorting := root.(*plan.SortNode)
	if !sorting && !bounded {
		return root
	}
	if sorting {
		base = ordered.CacheWidth
	}
	var late []expr.Expr
	for i, bound := range projection {
		if exec.QuerySetDepth(bound) != 0 {
			continue
		}
		if bound.ResultUsage().Type == nil {
			continue
		}
		switch bound.(type) {
		case plan.ColumnExpr, plan.GroupExpr, plan.CachedOrderExpr, plan.UnknownOutput, plan.LiteralExpr, plan.ParameterExpr:
			continue
		}
		if sorting && bound.Properties().Volatility != expr.Volatile {
			slot := ordered.CacheWidth
			ordered.Outputs = append(ordered.Outputs, bound)
			ordered.CacheWidth++
			projection[i] = plan.CachedOrderExpr{Info: expr.InfoOf(bound), Slot: slot}
		} else if bounded {
			late = append(late, bound)
			lateSlots = append(lateSlots, i)
		}
	}
	if sorting {
		base = ordered.CacheWidth
	}
	if len(late) == 0 {
		return root
	}
	for j, i := range lateSlots {
		bound := projection[i]
		projection[i] = plan.CachedOrderExpr{Info: expr.InfoOf(bound), Slot: base + j}
	}
	return &plan.EvaluationNode{QuerySchema: root.Schema(), Child: root, Expressions: late, Base: base}
}
