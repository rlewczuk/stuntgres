package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func ruleQueryFinalizer(view CatalogRead) func(LogicalNode) (LogicalNode, error) {
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
func finalizeRuleQuery(view CatalogRead, root LogicalNode, alreadyFinalized ...map[LogicalNode]bool) (LogicalNode, error) {
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
		if scan, ok := node.(*derivedNode); ok && scan.cte != nil && scan.cte.query != nil {
			retain(scan.cte.query.Root)
		}
	}, func(expr expr.Expr) {
		if subquery, ok := expr.(subqueryExpression); ok && subquery.cteDefinition {
			retain(subquery.queryPlan.Root)
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
			if scan, ok := node.(*derivedNode); ok && scan.cte != nil {
				copy := *scan
				return &copy, nil
			}
			project, ok := node.(*projectNode)
			if !ok {
				return node, nil
			}
			child, err := query(project.child)
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
					current = n.child
				case *rowLockNode:
					current = n.child
				case *evaluationNode:
					evaluation = n
					current = n.child
				case *distinctOnNode:
					distinct = n
					current = n.child
				case *setProjectNode:
					current = n.child
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
				copy.child = child
				return rewriteQueryExpressions(&copy, expression)
			}
			recipe := func(slot int) (expr.Expr, error) {
				if evaluation != nil && slot >= evaluation.base && slot < evaluation.base+len(evaluation.expressions) {
					return evaluation.expressions[slot-evaluation.base], nil
				}
				if ordered != nil {
					for _, key := range ordered.keys {
						if key.cache == slot {
							return key.expression, nil
						}
					}
					base := ordered.cacheWidth - len(ordered.outputs)
					if slot >= base && slot < ordered.cacheWidth {
						return ordered.outputs[slot-base], nil
					}
				}
				return nil, fmt.Errorf("stuntgres: invalid retained rule query cache")
			}
			recover := func(bound expr.Expr) (expr.Expr, bool, error) {
				return mapBoundExpression(view, bound, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, query, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
					if cached, ok := expr.(cachedOrderExpression); ok {
						next, err := recipe(cached.slot)
						return next, true, err
					}
					return expr, false, nil
				})
			}
			projection := slices.Clone(project.expressions)
			for i, expr := range projection {
				projection[i], _, err = recover(expr)
				if err != nil {
					return nil, err
				}
			}
			var order []sortKey
			if ordered != nil {
				order = slices.Clone(ordered.keys)
				for i := range order {
					order[i].slot, order[i].cache = -1, -1
					order[i].output = false
				}
			}
			var on []sortKey
			if distinct != nil {
				on = make([]sortKey, len(distinct.expressions))
				for i, expr := range distinct.expressions {
					next, _, failure := recover(expr)
					if failure != nil {
						return nil, failure
					}
					on[i] = sortKey{expression: next, slot: -1, cache: -1, targetIdentity: len(projection) + i + 1}
					for _, key := range order {
						if sameGroupExpression(next, key.expression) {
							on[i].targetIdentity = key.targetIdentity
							break
						}
					}
				}
			}
			var rebuild func(LogicalNode) (LogicalNode, error)
			rebuild = func(node LogicalNode) (LogicalNode, error) {
				if node == evaluation {
					return rebuild(evaluation.child)
				}
				if node == distinct {
					return rebuild(distinct.child)
				}
				if node == ordered || ordered == nil && node == stage {
					base := node
					if ordered != nil {
						base = ordered.child
					}
					var result LogicalNode
					var failure error
					binder := (*expressionBinder)(nil)
					if distinct != nil {
						result, failure = binder.finishDistinctOn(base, order, on, distinct.grouping, projection)
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
					copy.child, err = rebuild(n.child)
					if err != nil {
						return nil, err
					}
					copy.querySchema = querySchema(copy.child.Schema())
					copy.finishTies(copy.child)
					return &copy, nil
				case *rowLockNode:
					copy := *n
					copy.child, err = rebuild(n.child)
					if err != nil {
						return nil, err
					}
					copy.querySchema = querySchema(copy.child.Schema())
					return &copy, nil
				case *setProjectNode:
					copy := *n
					copy.child, err = rebuild(n.child)
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
			copy.child, copy.expressions = finished, projection
			return &copy, nil
		})
	}
	return query(root)
}
