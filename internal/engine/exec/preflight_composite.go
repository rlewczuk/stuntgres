package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// preflightComposite preflights a function node's children and then applies
// the fold the node names through expr.Preflighting.
func preflightComposite(c *Context, node expr.Composite, decisiveDefaults bool) (expr.Expr, bool, error) {
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
	call, sqlCall := node.(plan.SQLRoutineExpr)
	childIndex := -1
	mapped, changed, err := expr.MapSubstituted(node, func(child expr.Expr) (expr.Expr, bool, error) {
		childIndex++
		if sqlCall {
			local := *c
			local.defaultCall = nil
			if childIndex >= call.WrittenCount {
				local.SelectRoutineDefault(call.Ref, call.ChildSlots[childIndex], call.Procedure.SQLDefaults.Dependencies(), call.Info)
			}
			next, changed, err := PreflightCoercionsWithPolicy(&local, child, decisiveDefaults)
			c.PreparedDomainChecks = local.PreparedDomainChecks
			return next, changed, err
		}
		return PreflightCoercionsWithPolicy(c, child, decisiveDefaults)
	})
	if err != nil {
		return nil, false, err
	}
	if folded, did, handled, err := foldRandomNullInputs(c, mapped); handled {
		return folded, changed || did, err
	}
	next := mapped.(expr.Composite)
	switch kind {
	case expr.PreflightXPath:
		call := next.(functions.XMLXPath)
		// Planning dependence belongs to the analyzed call, before child
		// conversions materialize this execution's imported values.
		p := node.Properties()
		independent := !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion
		generic := c.PreserveParameterDemand && p.ParameterDependent
		eligible := independent && !generic && call.Properties().Volatility == expr.Immutable && !planningExecutionBarrier(c, call)
		routed := call.WithWrapperRoute(eligible)
		// A route-only replacement must survive callers' copy-on-change maps.
		changed = changed || routed.Wrapper != call.Wrapper
		call = routed
		if !independent || generic {
			return call, changed, nil
		}
		pruned, folded, err := preflightStrictParameterNull(c, call, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if folded {
			return pruned, true, nil
		}
		if !eligible {
			return call, changed, nil
		}
		value, err := FoldPlanningExpression(c, call)
		return preflightValue(c, expr.InfoOf(call), value, err)
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
		pruned, folded, err := preflightStrictParameterNull(c, next, decisiveDefaults)
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
	folded, simplified, err := SimplifyConditional(c, next, decisiveDefaults)
	return folded, changed || simplified, err
}

// preflightEvaluate folds an immutable call that depends on no row, stage or
// domain into its value.
func preflightEvaluate(c *Context, node expr.Expr, changed bool) (expr.Expr, bool, error) {
	p := node.Properties()
	if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion && !planningExecutionBarrier(c, node) {
		value, err := FoldPlanningExpression(c, node)
		return preflightValue(c, expr.InfoOf(node), value, err)
	}
	if call, ok := node.(functions.Regexp); ok {
		if inlined, did := call.InlineSQLBody(); did {
			return inlined, true, nil
		}
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
