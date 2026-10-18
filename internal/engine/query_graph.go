package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Logical inputs exclude expression-owned queries. Consumers visit those at
// explicit subquery boundaries, preserving lexical frames and leaf occurrences.
func queryInputs(node LogicalNode) (LogicalNode, LogicalNode) {
	switch n := node.(type) {
	case *setProjectNode:
		return n.child, nil
	case *setNode:
		return n.left, n.right
	case *recursiveUnionNode:
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
	case *distinctOnNode:
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
		if n.query != nil {
			return n.query.Root, nil
		}
	}
	return nil, nil
}

func queryExpressions(node LogicalNode, visit func(expr.Expr)) {
	args := func(items []expr.Expr) {
		for _, expr := range items {
			visit(expr)
		}
	}
	switch n := node.(type) {
	case *functionFromNode:
		for _, call := range n.calls {
			visit(call)
		}
	case *setProjectNode:
		for _, call := range n.calls {
			visit(call)
		}
		args(n.scalars)
		if n.template != nil {
			args(n.template.expressions)
			args(n.template.origins)
			args(n.template.scalars)
		}
	case *setNode:
		args(n.leftExpressions)
		args(n.rightExpressions)
	case *recursiveUnionNode:
		args(n.leftExpressions)
		args(n.rightExpressions)
		if c := n.cycle; c != nil {
			visit(c.mark)
			visit(c.fallback)
		}
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
		for _, call := range n.calls {
			if call.analysisOnly {
				continue
			}
			visit(call.argument)
			visit(call.ordinal)
			visit(call.defaultValue)
		}
		for i, spec := range n.specs {
			if !n.specActive(i) {
				continue
			}
			args(spec.partition)
			for _, key := range spec.order {
				visit(key.expression)
			}
			visit(spec.frame.start.offset)
			visit(spec.frame.end.offset)
		}
	case *aggregateNode:
		args(n.keys)
		for _, call := range n.calls {
			call.expressions(visit)
		}
	case *distinctNode:
		args(n.expressions)
	case *distinctOnNode:
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
		queryExpressions(node, func(bound expr.Expr) {
			walkBoundExpression(bound, func(expr expr.Expr) {
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
	queryExpressions(node, func(bound expr.Expr) {
		walkBoundExpression(bound, func(expr expr.Expr) {
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

// A type-only fast path must not retain an old routine identity carrier after
// catalog reownership. Pointer comparison here requests metadata reconstruction;
// it is never an ACL or lifetime admission decision.
func queryRoutineCarriersChanged(view CatalogRead, node LogicalNode) bool {
	if node == nil || view.state == nil {
		return false
	}
	changed := false
	queryExpressions(node, func(expression expr.Expr) {
		walkBoundExpression(expression, func(expression expr.Expr) {
			captured, err := expressionRoutineDescriptor(expression)
			if err != nil || captured != nil && view.Get(captured.address) != captured {
				changed = true
			}
			if subquery, ok := expression.(subqueryExpression); ok && queryRoutineCarriersChanged(view, subquery.queryPlan.Root) {
				changed = true
			}
		})
	})
	if changed {
		return true
	}
	left, right := queryInputs(node)
	return queryRoutineCarriersChanged(view, left) || queryRoutineCarriersChanged(view, right)
}

func queryIgnoresOwnExpressions(node LogicalNode) bool {
	switch n := node.(type) {
	case *sortNode:
		return n.presenceOnly
	case *aggregateNode:
		return n.presenceOnly
	case *distinctNode:
		return n.presenceOnly
	case *distinctOnNode:
		return n.presenceOnly
	}
	return false
}

func queryDemandProperties(project *projectNode, presence bool) expr.Properties {
	var properties expr.Properties
	var walk func(LogicalNode, *projectNode)
	walk = func(node LogicalNode, ignoredProject *projectNode) {
		if node == nil {
			return
		}
		if node != ignoredProject && !queryIgnoresOwnExpressions(node) {
			queryExpressions(node, func(bound expr.Expr) {
				if edge, ok := bound.(subqueryExpression); ok && edge.cteDefinition {
					return
				}
				if bound == nil {
					return
				}
				p := bound.Properties()
				p.RowDependent = false
				properties = expr.MergeProperties(properties, p)
				walkBoundExpression(bound, func(expr expr.Expr) {
					var frame uint32
					switch n := expr.(type) {
					case columnExpression:
						frame = n.frame
					case returningWholeRowExpression:
						frame = n.frame
					case mergeActionExpression:
						frame = n.frame
					case groupExpression:
						frame = n.frame
					case aggregateExpression:
						frame = n.frame
					case groupingExpression:
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

// Runtime reads belong to an occurrence's physical input span in its lexical
// frame, not to a relation OID. Analysis-only CTE edges remain frozen metadata.
func queryExecutionSourceReads(root LogicalNode, ignoredProject *projectNode) map[int][]int32 {
	type span struct {
		source *sourceNode
		offset int
	}
	var frames map[uint32][]span
	var reads map[int][]int32
	var walk func(LogicalNode, uint32, int, *projectNode, bool)
	walk = func(node LogicalNode, frame uint32, offset int, ignored *projectNode, collect bool) {
		if node == nil {
			return
		}
		if project, ok := node.(*projectNode); ok {
			frame, offset = project.frame, 0
			if frame == 0 {
				frame = 1
			}
		}
		if source, ok := node.(*sourceNode); ok && !collect {
			if reads == nil {
				frames = make(map[uint32][]span)
				reads = make(map[int][]int32)
			}
			frames[frame] = append(frames[frame], span{source, offset})
			reads[source.occurrence] = []int32{}
		}
		left, right := queryInputs(node)
		walk(left, frame, offset, ignored, collect)
		rightOffset := offset
		if join, ok := node.(*joinNode); ok {
			rightOffset += join.leftWidth
		}
		walk(right, frame, rightOffset, ignored, collect)
		if node == ignored || queryIgnoresOwnExpressions(node) {
			return
		}
		visit := func(expression expr.Expr) {
			walkBoundExpression(expression, func(expression expr.Expr) {
				switch e := expression.(type) {
				case subqueryExpression:
					if e.cteDefinition {
						return
					}
					var ignoredChild *projectNode
					if e.presenceOnly {
						ignoredChild = e.queryPlan.Root.(*projectNode)
					}
					walk(e.queryPlan.Root, frame, 0, ignoredChild, collect)
				case columnExpression:
					if !collect || e.scope != 1 || e.origin.Address.SubID <= 0 {
						return
					}
					for _, input := range frames[e.frame] {
						if e.slot < input.offset || e.slot >= input.offset+len(input.source.Schema()) || e.origin.Address.ClassID != input.source.ref.Address.ClassID || e.origin.Address.ObjectID != input.source.ref.Address.ObjectID {
							continue
						}
						items := reads[input.source.occurrence]
						for _, attribute := range items {
							if attribute == e.origin.Address.SubID {
								return
							}
						}
						reads[input.source.occurrence] = append(items, e.origin.Address.SubID)
						return
					}
				}
			})
		}
		if project, ok := node.(*projectNode); ok {
			for _, expression := range project.expressions {
				visit(expression)
			}
		} else {
			queryExpressions(node, visit)
		}
	}
	// Register all spans first, including outer frames referenced by correlated
	// expressions inside other inputs. Then collect only executable producers.
	walk(root, 1, 0, ignoredProject, false)
	if reads != nil {
		walk(root, 1, 0, ignoredProject, true)
	}
	return reads
}

// Bridge captured attribute identities to the current source layout. nil
// denotes full demand; a nonnil empty slice denotes presence-only demand.
func querySourceReadUses(reads []int32, attributes []int32) ([]int, error) {
	if reads == nil {
		return nil, nil
	}
	uses := make([]int, 0, len(reads))
	for slot, attribute := range attributes {
		for _, read := range reads {
			if attribute == read {
				uses = append(uses, slot)
				break
			}
		}
	}
	if len(uses) != len(reads) {
		return nil, ids.ErrCatalogReplaced
	}
	return uses, nil
}
