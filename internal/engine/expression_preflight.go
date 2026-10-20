package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A folded value retains conservative type/dependence metadata and the original
// binding retains its lifetime manifest, but the value is not a callable node.
func foldedExpressionInfo(node expr.Expr) expr.Info {
	info := expr.InfoOf(node)
	info.RoutineReference = nil
	return info
}

// preflightCoercions materializes immutable conversions from the imported
// execution frame, never rows or stages. Only changed branches are copied;
// analyzed nodes and their conservative dependence metadata remain immutable.
func preflightCoercions(c *exec.Context, expression expr.Expr) (expr.Expr, bool, error) {
	return preflightCoercionsWithPolicy(c, expression, false)
}

// Consumed stored defaults and standalone CHECK preparation use decisive
// constant planning. Ordinary source, DELETE and supplied-value preflight
// retain their existing traversal demand.
func preflightCoercionsWithPolicy(c *exec.Context, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	node, changed, err := preflightCoercionNode(c, expression, decisiveDefaults)
	if err != nil {
		return nil, false, err
	}
	node, folded, err := foldStrictParameterNull(c, node)
	return node, changed || folded, err
}

func preflightCoercionNode(c *exec.Context, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	if err := exec.EvaluationContext(c); err != nil {
		return nil, false, err
	}
	recipe, err := routine.ExpressionRecipeDescriptor(expression)
	if err != nil {
		return nil, false, err
	}
	if recipe != nil && (!c.RoutineGraft || c.RoutineConsumer != recipe) || recipe == nil && c.RoutineGraft {
		parent := c
		local := *c
		if err := selectExpressionRecipe(&local, recipe); err != nil {
			return nil, false, err
		}
		c = &local
		defer func() { parent.PreparedDomainChecks = c.PreparedDomainChecks }()
	}
	if c.SpecializeRuleParameters && expression != nil && expression.Properties().ParameterDependent {
		specialized, changed, err := specializeRuleParameterExpression(c, expression)
		if err != nil {
			return nil, false, err
		}
		if changed {
			folded, foldedChanged, err := preflightCoercionsWithPolicy(c, specialized, decisiveDefaults)
			return folded, changed || foldedChanged, err
		}
	}
	switch e := expression.(type) {
	case plan.SubqueryExpr:
		if !c.PlanStaticOperators || e.CTEEdge || e.QueryPlan == nil {
			return e, false, nil
		}
		input := e.QueryPlan.Root
		var presence *plan.ProjectNode
		if e.PresenceOnly {
			// Eligible EXISTS uses nextOccurrence, never its outer targets.
			// Keep actual child predicate and presence/sort preparation.
			presence = input.(*plan.ProjectNode)
			input = presence.Child
		}
		root, err := preflightQuery(c, input)
		if err != nil {
			return nil, false, err
		}
		if root == input {
			return e, false, nil
		}
		if presence != nil {
			copy := *presence
			copy.Child = root
			root = &copy
		}
		query := *e.QueryPlan
		query.Root = root
		e.QueryPlan = &query
		return e, true, nil
	case plan.DomainValueExpr:
		return e, false, nil
	case plan.InstalledSoundexExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Soundex = next
		}
		return e, args != nil, nil
	case plan.InstalledMetaphoneExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Metaphone = next
		}
		return e, args != nil, nil
	case plan.InstalledLevenshteinExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Levenshtein = next
		}
		return e, args != nil, nil
	case plan.InstalledUUIDOsspExpr:
		return e, false, nil
	case plan.InstalledCryptoExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			next, err := e.Reconstruct(e.Info, args)
			if err != nil {
				return nil, false, err
			}
			e.Pgcrypto = next
		}
		return e, args != nil, nil
	case plan.AssignmentValueExpr:
		base, baseChanged, err := preflightCoercionsWithPolicy(c, e.Base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		value, valueChanged, err := preflightCoercionsWithPolicy(c, e.Value, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		targets, err := preflightArrayTargetsWithPlanning(c, []*plan.ArrayAssignmentTarget{e.Target}, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		changed := baseChanged || valueChanged || targets[0] != e.Target
		if changed {
			e.Base, e.Value, e.Target = base, value, targets[0]
		}
		return e, changed, nil
	case plan.InstalledTextExpr:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.Arg, decisiveDefaults)
		if changed {
			e.Arg = arg
		}
		return e, changed, err
	case plan.ViewQualificationExpr:
		return mapViewQualification(e, func(child expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
		})
	case plan.GroupingExpr:
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.Args = args
		}
		return e, args != nil, nil
	case plan.ExtensionPathsExpr:
		arg, changed, err := preflightCoercionsWithPolicy(c, e.Arg, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Arg = arg
		return e, changed, nil
	case plan.CaseExpr:
		return simplifyConditional(c, expression, decisiveDefaults)
	case plan.ConditionalExpr:
		if err := e.ExtremaError(); err != nil {
			return nil, false, err
		}
		if e.Kind == parser.Coalesce {
			return simplifyConditional(c, expression, decisiveDefaults)
		}
		var args []expr.Expr
		for i, arg := range e.Args {
			next, changed, err := preflightCoercionsWithPolicy(c, arg, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			e.Args = args
		}
		next, changed, err := simplifyConditional(c, e, decisiveDefaults)
		return next, changed || args != nil, err
	case plan.RowExpr:
		next, changed, err := plan.MapRow(e, func(old expr.Expr) (expr.Expr, bool, error) {
			next, changed, err := simplifyConditional(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			folded, did, err := preflightCoercionsWithPolicy(c, next, decisiveDefaults)
			return folded, changed || did, err
		})
		return next, changed, err
	case plan.FieldExpr:
		base, changed, err := preflightCoercionsWithPolicy(c, e.Base, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Base = base
		return e, changed, nil
	case plan.ArrayExpr:
		var elements []expr.Expr
		for i, old := range e.Elements {
			next, changed, err := preflightCoercionsWithPolicy(c, old, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if elements == nil {
					elements = append([]expr.Expr(nil), e.Elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.Elements = elements
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, elements != nil, nil
	case plan.SubscriptExpr:
		next, changed, err := plan.MapSubscript(e, func(old expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, old, decisiveDefaults)
		})
		return next, changed, err
	case plan.CollationExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Child = child
		return e, changed, nil
	case plan.CoercionExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Child = child
		if e.Plan.ChecksDomain() {
			if err := prepareDomainConversion(c, e.Child.ResultUsage().Type, e.Usage.Type); err != nil {
				return nil, false, err
			}
		}
		// A function-method coercion is subject to the same execution-known
		// strict NULL pruning as other selected scalars. Do this after child
		// input/domain preflight but before constant computation admits EXECUTE.
		if folded, did, err := foldStrictParameterNull(c, e); err != nil || did {
			return folded, did, err
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if changed {
			return e, true, nil
		}
	case plan.LikeExpr:
		left, lc, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rc, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		e.Left, e.Right = left, right
		if exec.RandomLiteralNull(left) || exec.RandomLiteralNull(right) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		p := e.Properties()
		if p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion {
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		return e, lc || rc, nil
	case plan.RowComparisonExpr:
		var pairs []plan.ComparisonExpr
		for i, pair := range e.Comparisons {
			left, leftChanged, err := preflightCoercionsWithPolicy(c, pair.Left, false)
			if err != nil {
				return nil, false, err
			}
			right, rightChanged, err := preflightCoercionsWithPolicy(c, pair.Right, false)
			if err != nil {
				return nil, false, err
			}
			// The pair belongs to the row's NULL-aware/lexicographic frontier,
			// not an independently foldable scalar comparison.
			if leftChanged || rightChanged {
				if pairs == nil {
					pairs = append([]plan.ComparisonExpr(nil), e.Comparisons...)
				}
				pair.Left, pair.Right = left, right
				pairs[i] = pair
			}
		}
		if pairs != nil {
			e.Comparisons = pairs
			return e, true, nil
		}
		return e, false, nil
	case plan.ScalarArrayComparisonExpr:
		next, changed, err := plan.MapScalarArrayChildren(e, func(child expr.Expr) (expr.Expr, bool, error) {
			return preflightCoercionsWithPolicy(c, child, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults && conditionalConstantInputs(next) {
			value, err := evaluatePlanningExpression(c, next)
			return preflightValue(c, next.Info, value, err)
		}
		if changed {
			return next, true, nil
		}
	case plan.ComparisonExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(plan.LiteralExpr)
			_, rightConstant := right.(plan.LiteralExpr)
			if leftConstant && rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case plan.BitwiseExpr:
		left, lc, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right expr.Expr
		var rc bool
		if e.Right != nil {
			right, rc, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.Left, e.Right = left, right
		if bitwiseConstantNull(left) || bitwiseConstantNull(right) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		// Network operators follow the release's custom-plan Execute frontier:
		// imported parameters are available now, but retain their provenance.
		if e.Usage.Type.ID() == sqltype.Inet && networkOperatorExecutionConstant(e, c) {
			if folded, changed, err := foldStrictParameterNull(c, e); err != nil || changed {
				return folded, changed, err
			}
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if decisiveDefaults || e.Usage.Type.ID() == sqltype.Bit {
			_, l := left.(plan.LiteralExpr)
			_, r := right.(plan.LiteralExpr)
			p := e.Properties()
			if l && (right == nil || r) && p == (expr.Properties{}) {
				v, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, v, err)
			}
		}
		if lc || rc {
			return e, true, nil
		}
	case plan.SelectedOperatorExpr:
		provider, changed, err := preflightCoercionsWithPolicy(c, e.Provider, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if changed {
			e.Provider = provider
			return e, true, nil
		}
	case plan.ArithmeticExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var right expr.Expr
		var rightChanged bool
		if e.Right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		e.Left, e.Right = left, right
		if arithmeticSetConstantNull(expression.(plan.ArithmeticExpr), e) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		if (e.Left.ResultUsage().Type.ID() == sqltype.Inet || e.Right != nil && e.Right.ResultUsage().Type.ID() == sqltype.Inet) && networkOperatorExecutionConstant(e, c) {
			if folded, changed, err := foldStrictParameterNull(c, e); err != nil || changed {
				return folded, changed, err
			}
			value, err := evaluatePlanningExpression(c, e)
			return preflightValue(c, e.Info, value, err)
		}
		if decisiveDefaults || c.PlanStaticOperators {
			// Dependence belongs to the original analyzed recipe: an imported
			// cast may now be a literal without making its parameter static.
			p := expression.Properties()
			if p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion && conditionalConstantInputs(e) {
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		// Other consumers keep arithmetic at their existing row-owned frontier.
		if leftChanged || rightChanged {
			return e, true, nil
		}
	case plan.NullTestExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(plan.LiteralExpr); constant {
				e.Child = child
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if changed {
			e.Child = child
			return e, true, nil
		}
	case plan.TruthTestExpr:
		child, changed, err := preflightCoercionsWithPolicy(c, e.Child, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			if _, constant := child.(plan.LiteralExpr); constant {
				e.Child = child
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if changed {
			e.Child = child
			return e, true, nil
		}
	case plan.DistinctExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		right, rightChanged, err := preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		if decisiveDefaults {
			_, leftConstant := left.(plan.LiteralExpr)
			_, rightConstant := right.(plan.LiteralExpr)
			if leftConstant && rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case plan.LogicalExpr:
		left, leftChanged, err := preflightCoercionsWithPolicy(c, e.Left, decisiveDefaults)
		if err != nil {
			return nil, false, err
		}
		var leftConstant bool
		if decisiveDefaults {
			_, leftConstant = left.(plan.LiteralExpr)
			e.Left = left
			value, decisive, err := plan.DecisiveLogicalValue(&e)
			if err != nil {
				return nil, false, err
			}
			if decisive {
				return preflightValue(c, e.Info, value, nil)
			}
		}
		var right expr.Expr
		var rightChanged bool
		if e.Right != nil {
			right, rightChanged, err = preflightCoercionsWithPolicy(c, e.Right, decisiveDefaults)
			if err != nil {
				return nil, false, err
			}
		}
		if decisiveDefaults && leftConstant {
			_, rightConstant := right.(plan.LiteralExpr)
			if e.Op == parser.LogicalNot || rightConstant {
				e.Left, e.Right = left, right
				value, err := evaluatePlanningExpression(c, e)
				return preflightValue(c, e.Info, value, err)
			}
		}
		if leftChanged || rightChanged {
			e.Left, e.Right = left, right
			return e, true, nil
		}
	case expr.Composite:
		// Function nodes name their fold; installed-routine wrappers keep
		// their cases above.
		return preflightComposite(c, e, decisiveDefaults)
	}
	return expression, false, nil
}

func preflightValue(c *exec.Context, info expr.Info, value sqltype.Value, err error) (expr.Expr, bool, error) {
	if err != nil {
		return nil, false, err
	}
	if err := exec.EvaluationContext(c); err != nil {
		return nil, false, err
	}
	info.RoutineReference = nil
	return plan.LiteralExpr{Info: info, Value: value}, true, nil
}

// Strict arithmetic may erase a target-set subtree when a physical constant
// NULL makes its result independent of producer demand. Ordinary row arithmetic
// and imported parameter NULLs retain their existing execution-owned behavior.
func arithmeticSetConstantNull(original, current plan.ArithmeticExpr) bool {
	return (bitwiseConstantNull(current.Left) || bitwiseConstantNull(current.Right)) &&
		(exec.QuerySetDepth(original) != 0 || exec.QuerySetDepth(current) != 0)
}
