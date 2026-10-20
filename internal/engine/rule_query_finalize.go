package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func ruleQueryFinalizer(view catalog.Read) func(LogicalNode) (LogicalNode, error) {
	var finalized map[LogicalNode]bool
	return func(root LogicalNode) (LogicalNode, error) {
		result, err := finalizeRuleQuery(view, root, finalized)
		if err == nil {
			if finalized == nil {
				finalized = make(map[LogicalNode]bool)
			}
			finalized[result] = true
		}
		return result, err
	}
}

// Recover typed recipes from the existing schedule, then use the ordinary
// finalizers. Cache numbers belong to a query frame, never to a grafted row.
func finalizeRuleQuery(view catalog.Read, root LogicalNode, alreadyFinalized ...map[LogicalNode]bool) (LogicalNode, error) {
	var finalized map[LogicalNode]bool
	if len(alreadyFinalized) != 0 {
		finalized = alreadyFinalized[0]
	}
	// The whole-tree mapper finalizes changed action-owned WITH recipes before
	// their consumers. Protected originals and those finalized recipes are
	// provider boundaries, not a second scheduling pass.
	var providers map[LogicalNode]bool
	retain := func(root LogicalNode) {
		if root != nil {
			if providers == nil {
				providers = make(map[LogicalNode]bool)
			}
			providers[root] = true
		}
	}
	visitCTEQuery(root, func(node LogicalNode) {
		if scan, ok := node.(*derivedNode); ok && scan.CTE != nil && scan.CTE.Query != nil {
			retain(scan.CTE.Query.Root)
		}
	}, func(expr expr.Expr) {
		if subquery, ok := expr.(subqueryExpression); ok && subquery.cteDefinition {
			retain(subquery.QueryPlan.Root)
		}
	}, true)
	var query func(LogicalNode) (LogicalNode, error)
	var expression func(expr.Expr) (expr.Expr, bool, error)
	expression = func(expr expr.Expr) (expr.Expr, bool, error) {
		return mapBoundExpression(view, expr, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, query, nil, nil)
	}
	query = func(root LogicalNode) (LogicalNode, error) {
		if providers[root] || finalized[root] {
			return root, nil
		}
		return rewriteQueryExpressions(root, expression, func(node LogicalNode) (LogicalNode, error) {
			if finalized[node] {
				if project, ok := node.(*projectNode); ok {
					copy := *project
					return &copy, nil
				}
			}
			if scan, ok := node.(*derivedNode); ok && scan.CTE != nil {
				copy := *scan
				return &copy, nil
			}
			project, ok := node.(*projectNode)
			if !ok {
				return node, nil
			}
			child, err := query(project.Child)
			if err != nil {
				return nil, err
			}
			var ordered *sortNode
			var evaluation *evaluationNode
			var distinct *distinctOnNode
			bounded := false
			var stage LogicalNode
			for current := child; current != nil; {
				switch n := current.(type) {
				case *limitNode:
					bounded = true
					current = n.Child
				case *rowLockNode:
					current = n.Child
				case *evaluationNode:
					evaluation = n
					current = n.Child
				case *distinctOnNode:
					distinct = n
					current = n.Child
				case *setProjectNode:
					current = n.Child
				case *sortNode:
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
					if cached, ok := expr.(cachedOrderExpression); ok {
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
			var order []sortKey
			if ordered != nil {
				order = slices.Clone(ordered.Keys)
				for i := range order {
					order[i].Slot, order[i].Cache = -1, -1
					order[i].Output = false
				}
			}
			var on []sortKey
			if distinct != nil {
				on = make([]sortKey, len(distinct.Expressions))
				for i, expr := range distinct.Expressions {
					next, _, failure := recover(expr)
					if failure != nil {
						return nil, failure
					}
					on[i] = sortKey{Expression: next, Slot: -1, Cache: -1, TargetIdentity: len(projection) + i + 1}
					for _, key := range order {
						if sameGroupExpression(next, key.Expression) {
							on[i].TargetIdentity = key.TargetIdentity
							break
						}
					}
				}
			}
			var rebuild func(LogicalNode) (LogicalNode, error)
			rebuild = func(node LogicalNode) (LogicalNode, error) {
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
					var result LogicalNode
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
				case *limitNode:
					copy := *n
					copy.Child, err = rebuild(n.Child)
					if err != nil {
						return nil, err
					}
					copy.querySchema = querySchema(copy.Child.Schema())
					copy.FinishTies(copy.Child)
					return &copy, nil
				case *rowLockNode:
					copy := *n
					copy.Child, err = rebuild(n.Child)
					if err != nil {
						return nil, err
					}
					copy.querySchema = querySchema(copy.Child.Schema())
					return &copy, nil
				case *setProjectNode:
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
