package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Logical inputs exclude expression-owned queries. Consumers visit those at
// explicit subquery boundaries, preserving lexical frames and leaf occurrences.
func queryInputs(node LogicalNode) (LogicalNode, LogicalNode) {
	switch n := node.(type) {
	case *setNode:
		return n.left, n.right
	case *projectNode:
		return n.child, nil
	case *filterNode:
		return n.child, nil
	case *sortNode:
		return n.child, nil
	case *windowNode:
		return n.child, nil
	case *aggregateNode:
		return n.child, nil
	case *distinctNode:
		return n.child, nil
	case *limitNode:
		return n.child, nil
	case *rowLockNode:
		return n.child, nil
	case *evaluationNode:
		return n.child, nil
	case *joinNode:
		return n.left, n.right
	case *derivedNode:
		return n.query.Root, nil
	}
	return nil, nil
}

func queryExpressions(node LogicalNode, visit func(BoundExpr)) {
	args := func(items []BoundExpr) {
		for _, expr := range items {
			visit(expr)
		}
	}
	switch n := node.(type) {
	case *setNode:
		args(n.leftExpressions)
		args(n.rightExpressions)
	case *projectNode:
		args(n.expressions)
		args(n.analysisExpressions)
	case *filterNode:
		visit(n.predicate)
	case *sortNode:
		for _, key := range n.keys {
			visit(key.expression)
		}
		args(n.outputs)
	case *windowNode:
		for _, spec := range n.specs {
			args(spec.partition)
			for _, key := range spec.order {
				visit(key.expression)
			}
		}
	case *aggregateNode:
		args(n.keys)
		for _, call := range n.calls {
			call.expressions(visit)
		}
	case *distinctNode:
		args(n.expressions)
	case *limitNode:
		visit(n.offset)
		visit(n.count)
	case *evaluationNode:
		args(n.expressions)
	case *joinNode:
		visit(n.predicate)
		args(n.merges)
	case *valuesNode:
		for _, row := range n.rows {
			args(row)
		}
	}
}

// A stored view freezes every query's original input frame as well as the
// executable output tree. The maps are analyzed metadata, never row caches.
func collectQueryFrames(root LogicalNode) (map[uint32]*projectNode, map[*parser.Select]*projectNode) {
	frames := make(map[uint32]*projectNode)
	queries := make(map[*parser.Select]*projectNode)
	var walk func(LogicalNode)
	walk = func(node LogicalNode) {
		if node == nil {
			return
		}
		if n, ok := node.(*projectNode); ok && n.syntax != nil {
			if frames[n.frame] != nil {
				return
			}
			frames[n.frame], queries[n.syntax] = n, n
		}
		left, right := queryInputs(node)
		walk(left)
		walk(right)
		queryExpressions(node, func(expr BoundExpr) {
			walkBoundExpression(expr, func(expr BoundExpr) {
				if e, ok := expr.(subqueryExpression); ok {
					walk(e.queryPlan.Root)
				}
			})
		})
	}
	walk(root)
	return frames, queries
}

func queryDescriptorsChanged(node LogicalNode, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if node == nil {
		return false
	}
	for _, column := range node.Schema() {
		if typ(column.Usage.Type) != column.Usage.Type {
			return true
		}
	}
	changed := false
	queryExpressions(node, func(expr BoundExpr) {
		walkBoundExpression(expr, func(expr BoundExpr) {
			if typ(expr.ResultUsage().Type) != expr.ResultUsage().Type {
				changed = true
			}
			if child, ok := expr.(subqueryExpression); ok && queryDescriptorsChanged(child.queryPlan.Root, typ) {
				changed = true
			}
		})
	})
	if changed {
		return true
	}
	left, right := queryInputs(node)
	return queryDescriptorsChanged(left, typ) || queryDescriptorsChanged(right, typ)
}

func queryIgnoresOwnExpressions(node LogicalNode) bool {
	switch n := node.(type) {
	case *sortNode:
		return n.presenceOnly
	case *aggregateNode:
		return n.presenceOnly
	case *distinctNode:
		return n.presenceOnly
	}
	return false
}

func queryDemandProperties(project *projectNode, presence bool) ExprProperties {
	var properties ExprProperties
	var walk func(LogicalNode, *projectNode)
	walk = func(node LogicalNode, ignoredProject *projectNode) {
		if node == nil {
			return
		}
		if node != ignoredProject && !queryIgnoresOwnExpressions(node) {
			queryExpressions(node, func(expr BoundExpr) {
				if expr == nil {
					return
				}
				p := expr.Properties()
				p.RowDependent = false
				properties = mergeExpressionProperties(properties, p)
				walkBoundExpression(expr, func(expr BoundExpr) {
					var frame uint32
					switch n := expr.(type) {
					case columnExpression:
						frame = n.frame
					case groupExpression:
						frame = n.frame
					case aggregateExpression:
						frame = n.frame
					case *windowExpression:
						frame = n.frame
					case subqueryExpression:
						var ignoredChild *projectNode
						if n.presenceOnly {
							ignoredChild = n.queryPlan.Root.(*projectNode)
						}
						walk(n.queryPlan.Root, ignoredChild)
					}
					if frame != 0 && frame < project.frame {
						properties.RowDependent = true
					}
				})
			})
		}
		left, right := queryInputs(node)
		walk(left, ignoredProject)
		walk(right, ignoredProject)
	}
	var ignoredProject *projectNode
	if presence {
		ignoredProject = project
	}
	walk(project, ignoredProject)
	return properties
}
