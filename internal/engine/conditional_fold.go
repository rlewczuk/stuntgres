package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// simplifyCompositeNode simplifies a protocol node's children in order and
// applies its strict-NULL and fold hooks. done reports that the result skips
// the shared constant fold.
func simplifyCompositeNode(c *EvalContext, node expr.Composite, decisiveDefaults bool) (result BoundExpr, changed, done bool, err error) {
	strict := expr.StrictNone
	if hook, ok := node.(expr.StrictFolding); ok {
		strict = hook.StrictNull()
	}
	if strict == expr.StrictBefore && anyRandomLiteralNull(node.Children()) {
		return strictNullLiteral(node, strict), true, true, nil
	}
	next, changed, err := expr.Map(node, func(child BoundExpr) (BoundExpr, bool, error) {
		return simplifyConditional(c, child, decisiveDefaults)
	})
	if err != nil {
		return nil, false, true, err
	}
	if (strict == expr.StrictAfter || strict == expr.StrictAfterBare) && anyRandomLiteralNull(next.(expr.Composite).Children()) {
		return strictNullLiteral(node, strict), true, true, nil
	}
	switch planningFold(node) {
	case expr.FoldNever:
		return next, changed, true, nil
	case expr.FoldOverlapsRoute:
		// The engine owns OVERLAPS inline admission and its folded pair ends.
		routed, did, err := simplifyOverlapsRoute(c, next.(functions.DatetimeOverlaps))
		return routed, changed || did, false, err
	case expr.FoldQuoteCapture:
		// The engine owns the statement's quote-capture cache.
		folded, did, err := foldStringVariadic(c, next.(functions.StringVariadic))
		return folded, changed || did, true, err
	}
	return next, changed, false, nil
}

func planningFold(node BoundExpr) expr.Fold {
	if hook, ok := node.(expr.Folding); ok {
		return hook.PlanningFold()
	}
	return expr.FoldConstant
}

func anyRandomLiteralNull(children []BoundExpr) bool {
	for _, child := range children {
		if randomLiteralNull(child) {
			return true
		}
	}
	return false
}

func strictNullLiteral(node BoundExpr, strict expr.Strict) BoundExpr {
	usage := node.ResultUsage()
	if strict == expr.StrictAfterBare {
		return literalExpression{expressionInfo: expressionInfo{Usage: usage, Pos: node.SourcePosition()}, value: sqltype.Null(usage.Type)}
	}
	return literalExpression{foldedExpressionInfo(node), sqltype.Null(usage.Type)}
}

// compositeConstantInputs reports whether a protocol node that planning may
// fold has only literal children. Callers check volatility separately.
func compositeConstantInputs(node expr.Composite) bool {
	if planningFold(node) == expr.FoldNever {
		return false
	}
	for _, child := range node.Children() {
		if _, literal := child.(literalExpression); !literal {
			return false
		}
	}
	return true
}
