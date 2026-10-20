package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Logical inputs exclude expression-owned queries. Consumers visit those at
// explicit subquery boundaries, preserving lexical frames and leaf occurrences.
func queryInputs(node plan.Node) (plan.Node, plan.Node) {
	switch n := node.(type) {
	case *plan.SetProjectNode:
		return n.Child, nil
	case *plan.SetNode:
		return n.Left, n.Right
	case *plan.RecursiveUnionNode:
		return n.Left, n.Right
	case *plan.ProjectNode:
		return n.Child, nil
	case *plan.FilterNode:
		return n.Child, nil
	case *plan.SortNode:
		return n.Child, nil
	case *plan.WindowNode:
		return n.Child, nil
	case *plan.AggregateNode:
		return n.Child, nil
	case *plan.DistinctNode:
		return n.Child, nil
	case *plan.DistinctOnNode:
		return n.Child, nil
	case *plan.LimitNode:
		return n.Child, nil
	case *plan.RowLockNode:
		return n.Child, nil
	case *plan.EvaluationNode:
		return n.Child, nil
	case *plan.JoinNode:
		return n.Left, n.Right
	case *plan.DerivedNode:
		if n.Query != nil {
			return n.Query.Root, nil
		}
	}
	return nil, nil
}

func queryExpressions(node plan.Node, visit func(expr.Expr)) {
	args := func(items []expr.Expr) {
		for _, expr := range items {
			visit(expr)
		}
	}
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		for _, call := range n.Calls {
			visit(call)
		}
	case *plan.SetProjectNode:
		for _, call := range n.Calls {
			visit(call)
		}
		args(n.Scalars)
		if n.Template != nil {
			args(n.Template.Expressions)
			args(n.Template.Origins)
			args(n.Template.Scalars)
		}
	case *plan.SetNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
	case *plan.RecursiveUnionNode:
		args(n.LeftExpressions)
		args(n.RightExpressions)
		if c := n.Cycle; c != nil {
			visit(c.Mark)
			visit(c.Fallback)
		}
	case *plan.ProjectNode:
		args(n.Expressions)
		args(n.AnalysisExpressions)
	case *plan.FilterNode:
		visit(n.Predicate)
	case *plan.SortNode:
		for _, key := range n.Keys {
			visit(key.Expression)
		}
		args(n.Outputs)
	case *plan.WindowNode:
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
	case *plan.AggregateNode:
		args(n.Keys)
		for _, call := range n.Calls {
			call.Expressions(visit)
		}
	case *plan.DistinctNode:
		args(n.Expressions)
	case *plan.DistinctOnNode:
		args(n.Expressions)
	case *plan.LimitNode:
		visit(n.Offset)
		visit(n.Count)
	case *plan.EvaluationNode:
		args(n.Expressions)
	case *plan.JoinNode:
		visit(n.Predicate)
		args(n.Merges)
	case *plan.ValuesNode:
		for _, row := range n.Rows {
			args(row)
		}
	}
}

// A stored view freezes every query's original input frame as well as the
// executable output tree. The maps are analyzed metadata, never row caches.
func collectQueryFrames(root plan.Node) (map[uint32]*plan.ProjectNode, map[*parser.Select]*plan.ProjectNode) {
	frames := make(map[uint32]*plan.ProjectNode)
	queries := make(map[*parser.Select]*plan.ProjectNode)
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil {
			return
		}
		if n, ok := node.(*plan.ProjectNode); ok && n.Syntax != nil {
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
				if e, ok := expr.(plan.SubqueryExpr); ok {
					walk(e.QueryPlan.Root)
				}
			})
		})
	}
	walk(root)
	return frames, queries
}

func queryDescriptorsChanged(node plan.Node, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
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
			if child, ok := expr.(plan.SubqueryExpr); ok && queryDescriptorsChanged(child.QueryPlan.Root, typ) {
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
func queryRoutineCarriersChanged(view catalog.Read, node plan.Node) bool {
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
			if subquery, ok := expression.(plan.SubqueryExpr); ok && queryRoutineCarriersChanged(view, subquery.QueryPlan.Root) {
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

func queryIgnoresOwnExpressions(node plan.Node) bool {
	switch n := node.(type) {
	case *plan.SortNode:
		return n.PresenceOnly
	case *plan.AggregateNode:
		return n.PresenceOnly
	case *plan.DistinctNode:
		return n.PresenceOnly
	case *plan.DistinctOnNode:
		return n.PresenceOnly
	}
	return false
}

func queryDemandProperties(project *plan.ProjectNode, presence bool) expr.Properties {
	var properties expr.Properties
	var walk func(plan.Node, *plan.ProjectNode)
	walk = func(node plan.Node, ignoredProject *plan.ProjectNode) {
		if node == nil {
			return
		}
		if node != ignoredProject && !queryIgnoresOwnExpressions(node) {
			queryExpressions(node, func(bound expr.Expr) {
				if edge, ok := bound.(plan.SubqueryExpr); ok && edge.CTEDefinition {
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
					case plan.ColumnExpr:
						frame = n.Frame
					case plan.ReturningWholeRowExpr:
						frame = n.Frame
					case plan.MergeActionExpr:
						frame = n.Frame
					case plan.GroupExpr:
						frame = n.Frame
					case plan.AggregateExpr:
						frame = n.Frame
					case plan.GroupingExpr:
						frame = n.Frame
					case *plan.WindowExpr:
						frame = n.Frame
					case plan.SubqueryExpr:
						var ignoredChild *plan.ProjectNode
						if n.PresenceOnly {
							ignoredChild = n.QueryPlan.Root.(*plan.ProjectNode)
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
	var ignoredProject *plan.ProjectNode
	if presence {
		ignoredProject = project
	}
	walk(project, ignoredProject)
	return properties
}

// Runtime reads belong to an occurrence's physical input span in its lexical
// frame, not to a relation OID. Analysis-only CTE edges remain frozen metadata.
func queryExecutionSourceReads(root plan.Node, ignoredProject *plan.ProjectNode) map[int][]int32 {
	type span struct {
		source *plan.SourceNode
		offset int
	}
	var frames map[uint32][]span
	var reads map[int][]int32
	var walk func(plan.Node, uint32, int, *plan.ProjectNode, bool)
	walk = func(node plan.Node, frame uint32, offset int, ignored *plan.ProjectNode, collect bool) {
		if node == nil {
			return
		}
		if project, ok := node.(*plan.ProjectNode); ok {
			frame, offset = project.Frame, 0
			if frame == 0 {
				frame = 1
			}
		}
		if source, ok := node.(*plan.SourceNode); ok && !collect {
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
		if join, ok := node.(*plan.JoinNode); ok {
			rightOffset += join.LeftWidth
		}
		walk(right, frame, rightOffset, ignored, collect)
		if node == ignored || queryIgnoresOwnExpressions(node) {
			return
		}
		visit := func(expression expr.Expr) {
			walkBoundExpression(expression, func(expression expr.Expr) {
				switch e := expression.(type) {
				case plan.SubqueryExpr:
					if e.CTEDefinition {
						return
					}
					var ignoredChild *plan.ProjectNode
					if e.PresenceOnly {
						ignoredChild = e.QueryPlan.Root.(*plan.ProjectNode)
					}
					walk(e.QueryPlan.Root, frame, 0, ignoredChild, collect)
				case plan.ColumnExpr:
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
		if project, ok := node.(*plan.ProjectNode); ok {
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
