package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// simplifyCompositeNode simplifies a protocol node's children in order and
// applies its strict-NULL and fold hooks. done reports that the result skips
// the shared constant fold.
func simplifyCompositeNode(c *Context, node expr.Composite, decisiveDefaults bool) (result expr.Expr, changed, done bool, err error) {
	strict := expr.StrictNone
	if hook, ok := node.(expr.StrictFolding); ok {
		strict = hook.StrictNull()
	}
	if strict == expr.StrictBefore && anyRandomLiteralNull(node.Children()) {
		return strictNullLiteral(node, strict), true, true, nil
	}
	call, sqlCall := node.(plan.SQLRoutineExpr)
	childIndex := -1
	next, changed, err := expr.Map(node, func(child expr.Expr) (expr.Expr, bool, error) {
		childIndex++
		if sqlCall && childIndex >= call.WrittenCount {
			local := *c
			local.SelectRoutineDefault(call.Ref, call.ChildSlots[childIndex], call.Procedure.SQLDefaults.Dependencies(), call.Info)
			next, changed, err := SimplifyConditional(&local, child, decisiveDefaults)
			c.PreparedDomainChecks = local.PreparedDomainChecks
			return next, changed, err
		}
		return SimplifyConditional(c, child, decisiveDefaults)
	})
	if err != nil {
		return nil, false, true, err
	}
	if folded, did, handled, err := foldRandomNullInputs(c, next); handled {
		return folded, changed || did, true, err
	}
	if (strict == expr.StrictAfter || strict == expr.StrictAfterBare) && anyRandomLiteralNull(next.(expr.Composite).Children()) {
		return strictNullLiteral(node, strict), true, true, nil
	}
	switch planningFold(node) {
	case expr.FoldNever:
		return next, changed, true, nil
	case expr.FoldOverlapsRoute:
		// The engine owns OVERLAPS inline admission and its folded pair ends.
		routed, did, err := SimplifyOverlapsRoute(c, next.(functions.DatetimeOverlaps))
		return routed, changed || did, false, err
	}
	return next, changed, false, nil
}

func planningFold(node expr.Expr) expr.Fold {
	// A set source is an invocation, not a scalar constant, even when all
	// operands are literals. Keep the real node for shared target lifting.
	if expr.IsSetSource(node) {
		return expr.FoldNever
	}
	if hook, ok := node.(expr.Folding); ok {
		return hook.PlanningFold()
	}
	return expr.FoldConstant
}

func anyRandomLiteralNull(children []expr.Expr) bool {
	for _, child := range children {
		if RandomLiteralNull(child) {
			return true
		}
	}
	return false
}

func strictNullLiteral(node expr.Expr, strict expr.Strict) expr.Expr {
	usage := node.ResultUsage()
	if strict == expr.StrictAfterBare {
		return plan.LiteralExpr{Info: expr.Info{Usage: usage, Pos: node.SourcePosition()}, Value: sqltype.Null(usage.Type)}
	}
	return plan.LiteralExpr{Info: foldedExpressionInfo(node), Value: sqltype.Null(usage.Type)}
}

// compositeConstantInputs reports whether a protocol node that planning may
// fold has only literal children. Callers check volatility separately.
func compositeConstantInputs(node expr.Composite) bool {
	if planningFold(node) == expr.FoldNever {
		return false
	}
	for _, child := range node.Children() {
		if _, literal := child.(plan.LiteralExpr); child != nil && !literal {
			return false
		}
	}
	return true
}
