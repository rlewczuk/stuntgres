package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// preflightComposite preflights a function node's children and then applies
// the fold the node names through expr.Preflighting.
func preflightComposite(c *EvalContext, node expr.Composite, decisiveDefaults bool) (expr.Expr, bool, error) {
	kind := expr.PreflightSimplify
	if hook, ok := node.(expr.Preflighting); ok {
		kind = hook.PreflightFold()
	}
	switch kind {
	case expr.PreflightKeep:
		return node, false, nil
	case expr.PreflightNullFirst:
		if literalNullChild(node) {
			return plan.LiteralExpr{Info: foldedExpressionInfo(node), Value: sqltype.Null(node.ResultUsage().Type)}, true, nil
		}
	}
	mapped, changed, err := expr.MapSubstituted(node, func(child expr.Expr) (expr.Expr, bool, error) {
		return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
	})
	if err != nil {
		return nil, false, err
	}
	next := mapped.(expr.Composite)
	switch kind {
	case expr.PreflightChildren:
		return next, changed, nil
	case expr.PreflightNullBare:
		if literalNullChild(next) {
			u := next.ResultUsage()
			return plan.LiteralExpr{Info: expr.Info{Usage: u, Pos: next.SourcePosition()}, Value: sqltype.Null(u.Type)}, true, nil
		}
		return next, changed, nil
	case expr.PreflightNullOrEvaluate, expr.PreflightRegexNullOrEvaluate:
		if literalNullChild(next) {
			if kind == expr.PreflightRegexNullOrEvaluate {
				c.FunctionRegexExecution().Reset()
			}
			return preflightValue(c, expr.InfoOf(next), sqltype.Null(next.ResultUsage().Type), nil)
		}
		// Parameter expressions deliberately remain symbolic after child
		// preflight. Apply the ordinary typed-frame strict-NULL proof before
		// planning computation can admit a routine that will not be invoked.
		pruned, folded, err := foldStrictParameterNull(c, next)
		if err != nil {
			return nil, false, err
		}
		if folded {
			if kind == expr.PreflightRegexNullOrEvaluate {
				c.FunctionRegexExecution().Reset()
			}
			return pruned, true, nil
		}
		return preflightEvaluate(c, next, changed)
	case expr.PreflightEvaluate:
		return preflightEvaluate(c, next, changed)
	}
	if template, ok := next.(expr.ConstantTemplate); ok {
		specialized, did, err := template.SpecializeTemplate(c.Context, func(child expr.Expr) (sqltype.Value, bool) {
			literal, ok := child.(plan.LiteralExpr)
			return literal.Value, ok
		})
		if err != nil {
			return nil, false, err
		}
		if did {
			next, changed = specialized.(expr.Composite), true
		}
	}
	folded, simplified, err := simplifyConditional(c, next, decisiveDefaults)
	return folded, changed || simplified, err
}

// preflightEvaluate folds an immutable call that depends on no row, stage or
// domain into its value.
func preflightEvaluate(c *EvalContext, node expr.Expr, changed bool) (expr.Expr, bool, error) {
	p := node.Properties()
	if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
		value, err := evaluatePlanningExpression(c, node)
		return preflightValue(c, expr.InfoOf(node), value, err)
	}
	return node, changed, nil
}

func literalNullChild(node expr.Composite) bool {
	for _, child := range node.Children() {
		if RandomLiteralNull(child) {
			return true
		}
	}
	return false
}
