package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Logical inputs exclude expression-owned queries. Consumers visit those at
// explicit subquery boundaries, preserving lexical frames and leaf occurrences.
func queryInputs(node LogicalNode) (LogicalNode, LogicalNode) {
	switch n := node.(type) {
	case *setProjectNode:
		return n.Child, nil
	case *setNode:
		return n.Left, n.Right
	case *recursiveUnionNode:
		return n.Left, n.Right
	case *projectNode:
		return n.Child, nil
	case *filterNode:
		return n.Child, nil
	case *sortNode:
		return n.Child, nil
	case *windowNode:
		return n.Child, nil
	case *aggregateNode:
		return n.Child, nil
	case *distinctNode:
		return n.Child, nil
	case *distinctOnNode:
		return n.Child, nil
	case *limitNode:
		return n.Child, nil
	case *rowLockNode:
		return n.Child, nil
	case *evaluationNode:
		return n.Child, nil
	case *joinNode:
		return n.Left, n.Right
	case *derivedNode:
		if n.Query != nil {
			return n.Query.Root, nil
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
		for _, call := range n.Calls {
			visit(call)
		}
	case *setProjectNode:
		for _, call := range n.Calls {
			visit(call)
		}
		args(n.Scalars)
		if n.Template != nil {
			args(n.Template.Expressions)
			args(n.Template.Origins)
			args(n.Template.Scalars)
		}
	case *setNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
	case *recursiveUnionNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
		if c := n.Cycle; c != nil {
			visit(c.Mark)
			visit(c.Fallback)
		}
	case *projectNode:
		args(n.Expressions)
		args(n.AnalysisExpressions)
	case *filterNode:
		visit(n.Predicate)
	case *sortNode:
		for _, key := range n.Keys {
			visit(key.Expression)
		}
		args(n.Outputs)
	case *windowNode:
		for _, call := range n.Calls {
			if call.AnalysisOnly {
				continue
			}
			visit(call.Argument)
			visit(call.Ordinal)
			visit(call.DefaultValue)
		}
		for i, spec := range n.Specs {
			if !n.SpecActive(i) {
				continue
			}
			args(spec.Partition)
			for _, key := range spec.Order {
				visit(key.Expression)
			}
			visit(spec.Frame.Start.Offset)
			visit(spec.Frame.End.Offset)
		}
	case *aggregateNode:
		args(n.Keys)
		for _, call := range n.Calls {
			call.Expressions(visit)
		}
	case *distinctNode:
		args(n.Expressions)
	case *distinctOnNode:
		args(n.Expressions)
	case *limitNode:
		visit(n.Offset)
		visit(n.Count)
	case *evaluationNode:
		args(n.Expressions)
	case *joinNode:
		visit(n.Predicate)
		args(n.Merges)
	case *valuesNode:
		for _, row := range n.Rows {
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
		if n, ok := node.(*projectNode); ok && n.Syntax != nil {
			if frames[n.Frame] != nil {
				return
			}
			frames[n.Frame], queries[n.Syntax] = n, n
		}
		left, right := queryInputs(node)
		walk(left)
		walk(right)
		queryExpressions(node, func(bound expr.Expr) {
			walkBoundExpression(bound, func(expr expr.Expr) {
				if e, ok := expr.(subqueryExpression); ok {
					walk(e.QueryPlan.Root)
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
			if child, ok := expr.(subqueryExpression); ok && queryDescriptorsChanged(child.QueryPlan.Root, typ) {
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
func queryRoutineCarriersChanged(view catalog.Read, node LogicalNode) bool {
	if node == nil || view.State() == nil {
		return false
	}
	changed := false
	queryExpressions(node, func(expression expr.Expr) {
		walkBoundExpression(expression, func(expression expr.Expr) {
			captured, err := routine.ExpressionRoutineDescriptor(expression)
			if err != nil || captured != nil && view.Get(captured.Address()) != captured {
				changed = true
			}
			if subquery, ok := expression.(subqueryExpression); ok && queryRoutineCarriersChanged(view, subquery.QueryPlan.Root) {
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
		return n.PresenceOnly
	case *aggregateNode:
		return n.PresenceOnly
	case *distinctNode:
		return n.PresenceOnly
	case *distinctOnNode:
		return n.PresenceOnly
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
				if edge, ok := bound.(subqueryExpression); ok && edge.CTEDefinition {
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
						frame = n.Frame
					case returningWholeRowExpression:
						frame = n.Frame
					case mergeActionExpression:
						frame = n.Frame
					case groupExpression:
						frame = n.Frame
					case aggregateExpression:
						frame = n.Frame
					case groupingExpression:
						frame = n.Frame
					case *windowExpression:
						frame = n.Frame
					case subqueryExpression:
						var ignoredChild *projectNode
						if n.PresenceOnly {
							ignoredChild = n.QueryPlan.Root.(*projectNode)
						}
						walk(n.QueryPlan.Root, ignoredChild)
					}
					if frame != 0 && frame < project.Frame {
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
			frame, offset = project.Frame, 0
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
			reads[source.Occurrence] = []int32{}
		}
		left, right := queryInputs(node)
		walk(left, frame, offset, ignored, collect)
		rightOffset := offset
		if join, ok := node.(*joinNode); ok {
			rightOffset += join.LeftWidth
		}
		walk(right, frame, rightOffset, ignored, collect)
		if node == ignored || queryIgnoresOwnExpressions(node) {
			return
		}
		visit := func(expression expr.Expr) {
			walkBoundExpression(expression, func(expression expr.Expr) {
				switch e := expression.(type) {
				case subqueryExpression:
					if e.CTEDefinition {
						return
					}
					var ignoredChild *projectNode
					if e.PresenceOnly {
						ignoredChild = e.QueryPlan.Root.(*projectNode)
					}
					walk(e.QueryPlan.Root, frame, 0, ignoredChild, collect)
				case columnExpression:
					if !collect || e.Scope != 1 || e.Origin.Address.SubID <= 0 {
						return
					}
					for _, input := range frames[e.Frame] {
						if e.Slot < input.offset || e.Slot >= input.offset+len(input.source.Schema()) || e.Origin.Address.ClassID != input.source.Ref.Address.ClassID || e.Origin.Address.ObjectID != input.source.Ref.Address.ObjectID {
							continue
						}
						items := reads[input.source.Occurrence]
						for _, attribute := range items {
							if attribute == e.Origin.Address.SubID {
								return
							}
						}
						reads[input.source.Occurrence] = append(items, e.Origin.Address.SubID)
						return
					}
				}
			})
		}
		if project, ok := node.(*projectNode); ok {
			for _, expression := range project.Expressions {
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
