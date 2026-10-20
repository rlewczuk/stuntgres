package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ruleQueryFinalizer(view catalog.Read) func(plan.Node) (plan.Node, error) {
	var finalized map[plan.Node]bool
	return func(root plan.Node) (plan.Node, error) {
		result, err := finalizeRuleQuery(view, root, finalized)
		if err == nil {
			if finalized == nil {
				finalized = make(map[plan.Node]bool)
			}
			finalized[result] = true
		}
		return result, err
	}
}

// Recover typed recipes from the existing schedule, then use the ordinary
// finalizers. Cache numbers belong to a query frame, never to a grafted row.
func finalizeRuleQuery(view catalog.Read, root plan.Node, alreadyFinalized ...map[plan.Node]bool) (plan.Node, error) {
	var finalized map[plan.Node]bool
	if len(alreadyFinalized) != 0 {
		finalized = alreadyFinalized[0]
	}
	// The whole-tree mapper finalizes changed action-owned WITH recipes before
	// their consumers. Protected originals and those finalized recipes are
	// provider boundaries, not a second scheduling pass.
	var providers map[plan.Node]bool
	retain := func(root plan.Node) {
		if root != nil {
			if providers == nil {
				providers = make(map[plan.Node]bool)
			}
			providers[root] = true
		}
	}
	visitCTEQuery(root, func(node plan.Node) {
		if scan, ok := node.(*plan.DerivedNode); ok && scan.CTE != nil && scan.CTE.Query != nil {
			retain(scan.CTE.Query.Root)
		}
	}, func(expr expr.Expr) {
		if subquery, ok := expr.(plan.SubqueryExpr); ok && subquery.CTEDefinition {
			retain(subquery.QueryPlan.Root)
		}
	}, true)
	var query func(plan.Node) (plan.Node, error)
	var expression func(expr.Expr) (expr.Expr, bool, error)
	expression = func(expr expr.Expr) (expr.Expr, bool, error) {
		return mapBoundExpression(view, expr, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, query, nil, nil)
	}
	query = func(root plan.Node) (plan.Node, error) {
		if providers[root] || finalized[root] {
			return root, nil
		}
		return rewriteQueryExpressions(root, expression, func(node plan.Node) (plan.Node, error) {
			if finalized[node] {
				if project, ok := node.(*plan.ProjectNode); ok {
					copy := *project
					return &copy, nil
				}
			}
			if scan, ok := node.(*plan.DerivedNode); ok && scan.CTE != nil {
				copy := *scan
				return &copy, nil
			}
			project, ok := node.(*plan.ProjectNode)
			if !ok {
				return node, nil
			}
			child, err := query(project.Child)
			if err != nil {
				return nil, err
			}
			var ordered *plan.SortNode
			var evaluation *plan.EvaluationNode
			var distinct *plan.DistinctOnNode
			bounded := false
			var stage plan.Node
			for current := child; current != nil; {
				switch n := current.(type) {
				case *plan.LimitNode:
					bounded = true
					current = n.Child
				case *plan.RowLockNode:
					current = n.Child
				case *plan.EvaluationNode:
					evaluation = n
					current = n.Child
				case *plan.DistinctOnNode:
					distinct = n
					current = n.Child
				case *plan.SetProjectNode:
					current = n.Child
				case *plan.SortNode:
					ordered = n
					current = nil
				default:
					stage = current
					current = nil
				}
			}
			if ordered == nil && evaluation == nil && !bounded {
				copy := *project
				copy.Child = child
				return rewriteQueryExpressions(&copy, expression)
			}
			recipe := func(slot int) (expr.Expr, error) {
				if evaluation != nil && slot >= evaluation.Base && slot < evaluation.Base+len(evaluation.Expressions) {
					return evaluation.Expressions[slot-evaluation.Base], nil
				}
				if ordered != nil {
					for _, key := range ordered.Keys {
						if key.Cache == slot {
							return key.Expression, nil
						}
					}
					base := ordered.CacheWidth - len(ordered.Outputs)
					if slot >= base && slot < ordered.CacheWidth {
						return ordered.Outputs[slot-base], nil
					}
				}
				return nil, fmt.Errorf("stuntgres: invalid retained rule query cache")
			}
			recover := func(bound expr.Expr) (expr.Expr, bool, error) {
				return mapBoundExpression(view, bound, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, query, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
					if cached, ok := expr.(plan.CachedOrderExpr); ok {
						next, err := recipe(cached.Slot)
						return next, true, err
					}
					return expr, false, nil
				})
			}
			projection := slices.Clone(project.Expressions)
			for i, expr := range projection {
				projection[i], _, err = recover(expr)
				if err != nil {
					return nil, err
				}
			}
			var order []plan.SortKey
			if ordered != nil {
				order = slices.Clone(ordered.Keys)
				for i := range order {
					order[i].Slot, order[i].Cache = -1, -1
					order[i].Output = false
				}
			}
			var on []plan.SortKey
			if distinct != nil {
				on = make([]plan.SortKey, len(distinct.Expressions))
				for i, expr := range distinct.Expressions {
					next, _, failure := recover(expr)
					if failure != nil {
						return nil, failure
					}
					on[i] = plan.SortKey{Expression: next, Slot: -1, Cache: -1, TargetIdentity: len(projection) + i + 1}
					for _, key := range order {
						if sameGroupExpression(next, key.Expression) {
							on[i].TargetIdentity = key.TargetIdentity
							break
						}
					}
				}
			}
			var rebuild func(plan.Node) (plan.Node, error)
			rebuild = func(node plan.Node) (plan.Node, error) {
				if node == evaluation {
					return rebuild(evaluation.Child)
				}
				if node == distinct {
					return rebuild(distinct.Child)
				}
				if node == ordered || ordered == nil && node == stage {
					base := node
					if ordered != nil {
						base = ordered.Child
					}
					var result plan.Node
					var failure error
					binder := (*expressionBinder)(nil)
					if distinct != nil {
						result, failure = binder.finishDistinctOn(base, order, on, distinct.Grouping, projection)
					} else {
						result, failure = binder.finishOrder(base, order, projection)
					}
					if failure != nil {
						return nil, failure
					}
					if distinct == nil {
						result = binder.scheduleQueryProjection(result, projection, bounded)
					}
					return result, nil
				}
				switch n := node.(type) {
				case *plan.LimitNode:
					copy := *n
					copy.Child, err = rebuild(n.Child)
					if err != nil {
						return nil, err
					}
					copy.QuerySchema = plan.QuerySchema(copy.Child.Schema())
					copy.FinishTies(copy.Child)
					return &copy, nil
				case *plan.RowLockNode:
					copy := *n
					copy.Child, err = rebuild(n.Child)
					if err != nil {
						return nil, err
					}
					copy.QuerySchema = plan.QuerySchema(copy.Child.Schema())
					return &copy, nil
				case *plan.SetProjectNode:
					copy := *n
					copy.Child, err = rebuild(n.Child)
					if err != nil {
						return nil, err
					}
					return &copy, nil
				}
				return node, nil
			}
			finished, err := rebuild(child)
			if err != nil {
				return nil, err
			}
			copy := *project
			copy.Child, copy.Expressions = finished, projection
			return &copy, nil
		})
	}
	return query(root)
}
