package analyze

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryUsesFrame(node plan.Node, frame uint32) bool {
	if node == nil {
		return false
	}
	found := false
	plan.QueryExpressions(node, func(expr expr.Expr) {
		if ExpressionUsesFrame(expr, frame) {
			found = true
		}
	})
	if found {
		return true
	}
	left, right := plan.QueryInputs(node)
	return queryUsesFrame(left, frame) || queryUsesFrame(right, frame)
}

// ExpressionUsesFrame reports whether bound reads a column of query frame
// frame, including through nested subqueries.
func ExpressionUsesFrame(bound expr.Expr, frame uint32) bool {
	found := false
	plan.WalkExpression(bound, func(expr expr.Expr) {
		switch e := expr.(type) {
		case plan.ColumnExpr:
			found = found || e.Frame == frame
		case plan.ReturningWholeRowExpr:
			found = found || e.Frame == frame
		case plan.MergeActionExpr:
			found = found || e.Frame == frame
		case plan.AggregateExpr:
			found = found || e.Frame == frame
		case plan.GroupingExpr:
			found = found || e.Frame == frame
		case *plan.WindowExpr:
			found = found || e.Frame == frame
		case plan.GroupExpr:
			found = found || e.Frame == frame
		case plan.SubqueryExpr:
			found = found || queryUsesFrame(e.QueryPlan.Root, frame)
		}
	})
	return found
}

// FirstQueryBoundFrameVariable returns the position of the first column of
// frame that a LIMIT or OFFSET expression reads, or the zero position.
func FirstQueryBoundFrameVariable(bound expr.Expr, frame uint32) parser.Position {
	var position parser.Position
	found := false
	var query func(plan.Node)
	var expression func(expr.Expr)
	expression = func(bound expr.Expr) {
		plan.WalkExpression(bound, func(expr expr.Expr) {
			matched := false
			switch e := expr.(type) {
			case plan.ColumnExpr:
				matched = e.Frame == frame
			case plan.ReturningWholeRowExpr:
				matched = e.Frame == frame
			case plan.MergeActionExpr:
				matched = e.Frame == frame
			case plan.GroupExpr:
				matched = e.Frame == frame
			case plan.AggregateExpr:
				matched = e.Frame == frame
			case plan.GroupingExpr:
				matched = e.Frame == frame
			case *plan.WindowExpr:
				matched = e.Frame == frame
			case plan.SubqueryExpr:
				query(e.QueryPlan.Root)
			}
			if matched {
				p := expr.SourcePosition()
				if !found || p.Character < position.Character {
					position = p
					found = true
				}
			}
		})
	}
	query = func(node plan.Node) {
		if node == nil {
			return
		}
		plan.QueryExpressions(node, expression)
		left, right := plan.QueryInputs(node)
		query(left)
		query(right)
	}
	expression(bound)
	return position
}

// Aggregate ownership crosses sublinks, but only columns/aggregates in a
// visible ancestor frame participate. Child-local frames are ignored by the
// owner selection above; nesting is checked against that selected owner.
func WalkQueryArgumentExpression(bound expr.Expr, visit func(expr.Expr)) {
	plan.WalkExpression(bound, func(node expr.Expr) {
		visit(node)
		if child, ok := node.(plan.SubqueryExpr); ok {
			WalkQueryArgumentNode(child.QueryPlan.Root, visit)
		}
	})
}

// WalkQueryArgumentNode applies WalkQueryArgumentExpression to every
// expression of a query plan.
func WalkQueryArgumentNode(node plan.Node, visit func(expr.Expr)) {
	plan.QueryExpressions(node, func(expr expr.Expr) { WalkQueryArgumentExpression(expr, visit) })
	left, right := plan.QueryInputs(node)
	if left != nil {
		WalkQueryArgumentNode(left, visit)
	}
	if right != nil {
		WalkQueryArgumentNode(right, visit)
	}
}

// Hidden parent projections use the actual joined worktable occurrence, never
// generation order. The legality walker guarantees exactly one top-level self.
func RecursiveParentSlot(node plan.Node, d *plan.CTEDefinition, offset int) (*plan.WorktableScanNode, int) {
	switch n := node.(type) {
	case *plan.WorktableScanNode:
		if n.CTE == d {
			return n, offset
		}
	case *plan.JoinNode:
		if scan, slot := RecursiveParentSlot(n.Left, d, offset); scan != nil {
			return scan, slot
		}
		return RecursiveParentSlot(n.Right, d, offset+n.LeftWidth)
	default:
		left, _ := plan.QueryInputs(node)
		if left != nil {
			return RecursiveParentSlot(left, d, offset)
		}
	}
	return nil, 0
}

// CTEVolatile reports whether a CTE body is volatile on its own: it has a
// volatile expression, a row lock or a read of pg_sequences.
func CTEVolatile(root plan.Node) bool {
	volatile := false
	plan.VisitCTEQuery(root, func(n plan.Node) {
		if source, ok := n.(*plan.SourceNode); ok && source.Ref.Address.ClassID == ids.RelationClassID && source.Ref.Address.ObjectID == 12048 {
			volatile = true
		}
		for _, mark := range RootRowMarks(n) {
			if mark.Mode != 0 {
				volatile = true
			}
		}
	}, func(bound expr.Expr) {
		if bound.Properties().Volatility == expr.Volatile {
			volatile = true
		}
	}, true)
	return volatile
}

// MutationBinding recovers the analyzed binding of a data-modifying CTE.
func MutationBinding(d *plan.CTEDefinition) (*Binding, bool) {
	b, ok := d.Mutation.(*Binding)
	return b, ok && b != nil
}

// QueryIgnoresOwnExpressions reports whether node is a presence-only sort,
// aggregate or DISTINCT node, whose own expressions add no demand.
func QueryIgnoresOwnExpressions(node plan.Node) bool {
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

// QueryDemandProperties are the expression properties a subquery's
// projection demands of its outer query.
func QueryDemandProperties(project *plan.ProjectNode, presence bool) expr.Properties {
	var properties expr.Properties
	var walk func(plan.Node, *plan.ProjectNode)
	walk = func(node plan.Node, ignoredProject *plan.ProjectNode) {
		if node == nil {
			return
		}
		if node != ignoredProject && !QueryIgnoresOwnExpressions(node) {
			plan.QueryExpressions(node, func(bound expr.Expr) {
				if edge, ok := bound.(plan.SubqueryExpr); ok && edge.CTEEdge {
					return
				}
				if bound == nil {
					return
				}
				p := bound.Properties()
				p.RowDependent = false
				properties = expr.MergeProperties(properties, p)
				plan.WalkExpression(bound, func(expr expr.Expr) {
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
		left, right := plan.QueryInputs(node)
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

// Full analysis precedes this demand decision. A represented OFFSET, actual local
// aggregate, HAVING or rowmark retains ordinary tuple projection.
// Only a proven positive/NULL literal LIMIT is removable; parameters are not
// evaluated during binding. Retain all other plans conservatively.
func CanSimplifyExists(query *plan.Query) bool {
	project := query.Root.(*plan.ProjectNode)
	s := project.Syntax
	if s != nil && (s.Having != nil || s.Locking != nil || s.Bounds != nil && s.Bounds.Offset != nil) {
		return false
	}
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.SetProjectNode:
			return false // Target sets, including terminal attempts, own cardinality.
		case *plan.SetNode, *plan.RecursiveUnionNode:
			return false // Set keys determine presence, not just projected payload.
		case *plan.SourceNode, *plan.SingletonNode, *plan.ValuesNode, *plan.DerivedNode, *plan.WorktableScanNode, *plan.JoinNode, *plan.FunctionFromNode:
			return true // FROM inputs retain their own demand and clause fences.
		case *plan.WindowNode:
			return false // Keep real window-key demand and eager failure frontiers.
		case *plan.AggregateNode:
			if !n.DiscardableGrouping() {
				return false
			}
		case *plan.RowLockNode:
			return false
		case *plan.LimitNode:
			if n.Offset != nil {
				return false
			}
			if n.Count != nil {
				expr := n.Count
				for {
					c, ok := expr.(plan.CoercionExpr)
					if !ok {
						break
					}
					expr = c.Child
				}
				literal, ok := expr.(plan.LiteralExpr)
				if !ok {
					return false
				}
				if !literal.Value.IsNull() {
					count, err := literal.Value.Integer()
					if err != nil || count <= 0 {
						return false
					}
				}
			}
		}
		left, right := plan.QueryInputs(node)
		if right != nil {
			break
		} // FROM inputs are not the root's clause fences.
		node = left
	}
	return true
}

// Presence demands keep the fully analyzed graphs while dropping root target,
// plain grouping, DISTINCT and ORDER demand for eligible EXISTS only.
func PresenceQueryPlan(query *plan.Query) (*plan.Query, error) {
	project := query.Root.(*plan.ProjectNode)
	expressions := project.Expressions
	for i, bound := range project.Expressions {
		cached, ok := bound.(plan.CachedOrderExpr)
		if !ok {
			continue
		}
		original := scheduledQueryTarget(project.Child, cached.Slot)
		if original == nil {
			return nil, fmt.Errorf("stuntgres: missing analyzed presence target")
		}
		if &expressions[0] == &project.Expressions[0] {
			expressions = append([]expr.Expr(nil), expressions...)
		}
		expressions[i] = original
	}
	child := PresenceQueryInput(project.Child)
	if child == project.Child && (len(expressions) == 0 || &expressions[0] == &project.Expressions[0]) {
		return query, nil
	}
	copy := *project
	copy.Child, copy.Expressions = child, expressions
	plan := *query
	plan.Root = &copy
	return &plan, nil
}

func scheduledQueryTarget(node plan.Node, slot int) expr.Expr {
	for node != nil {
		switch n := node.(type) {
		case *plan.EvaluationNode:
			if slot >= n.Base && slot-n.Base < len(n.Expressions) {
				return n.Expressions[slot-n.Base]
			}
		case *plan.SortNode:
			base := n.CacheWidth - len(n.Outputs)
			if slot >= base && slot-base < len(n.Outputs) {
				return n.Outputs[slot-base]
			}
			for _, key := range n.Keys {
				if key.Cache == slot {
					return key.Expression
				}
			}
		}
		switch n := node.(type) {
		case *plan.EvaluationNode:
			node = n.Child
		case *plan.SortNode:
			node = n.Child
		case *plan.FilterNode:
			node = n.Child
		case *plan.AggregateNode:
			node = n.Child
		case *plan.WindowNode:
			node = n.Child
		case *plan.SetProjectNode:
			node = n.Child
		case *plan.DistinctNode:
			node = n.Child
		case *plan.DistinctOnNode:
			node = n.Child
		case *plan.RowLockNode:
			node = n.Child
		case *plan.LimitNode:
			node = n.Child
		default:
			return nil
		}
	}
	return nil
}

// PresenceQueryInput rewrites a query input for a presence test: evaluation
// nodes are dropped and sorts, DISTINCT and discardable grouping become
// presence-only copies; other nodes are kept.
func PresenceQueryInput(node plan.Node) plan.Node {
	switch n := node.(type) {
	case *plan.EvaluationNode:
		return PresenceQueryInput(n.Child)
	case *plan.SortNode:
		copy := *n
		copy.Child = PresenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctNode:
		copy := *n
		copy.Child = PresenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.Child = PresenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.FilterNode:
		child := PresenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.AggregateNode:
		if n.DiscardableGrouping() {
			copy := *n
			copy.Child = PresenceQueryInput(n.Child)
			copy.PresenceOnly = true
			return &copy
		}
		child := PresenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.RowLockNode:
		child := PresenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.LimitNode:
		copy := *n
		copy.Child = PresenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	default:
		return node
	}
}

// SQL99 compares analyzed RTE alias namespaces as well as resolved expressions.
// Keep only that identity from the retained syntax: Var qualification, source
// positions and raw predicates are not semantic comparison authorities.
func SameQuerySourceAliases(a, b parser.FromItem) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch n := a.(type) {
	case *parser.JSONTable:
		o, ok := b.(*parser.JSONTable)
		return ok && n.Lateral == o.Lateral
	case *parser.FunctionTable:
		o, ok := b.(*parser.FunctionTable)
		return ok && n.RowsFrom == o.RowsFrom
	case *parser.AliasReference:
		o, ok := b.(*parser.AliasReference)
		if !ok || n.Name.Text != o.Name.Text || len(n.Columns) != len(o.Columns) {
			return false
		}
		for i, name := range n.Columns {
			if name.Text != o.Columns[i].Text {
				return false
			}
		}
		return SameQuerySourceAliases(n.Input, o.Input)
	case *parser.JoinReference:
		o, ok := b.(*parser.JoinReference)
		if !ok {
			return false
		}
		if n.UsingAlias == nil || o.UsingAlias == nil {
			if n.UsingAlias != o.UsingAlias {
				return false
			}
		} else if n.UsingAlias.Text != o.UsingAlias.Text {
			return false
		}
		return SameQuerySourceAliases(n.Left, o.Left) && SameQuerySourceAliases(n.Right, o.Right)
	case *parser.TableReference:
		_, ok := b.(*parser.TableReference)
		return ok
	case *parser.DerivedQuery:
		_, ok := b.(*parser.DerivedQuery)
		return ok
	case *parser.ValuesQuery:
		_, ok := b.(*parser.ValuesQuery)
		return ok
	case *parser.FunctionFrom:
		o, ok := b.(*parser.FunctionFrom)
		return ok && n.Ordinality == o.Ordinality && n.Lateral == o.Lateral && len(n.Calls) == len(o.Calls)
	default:
		return false
	}
}

// PrivateTreeGrouping reports whether d, through domains and arrays, is
// pg_node_tree.
func PrivateTreeGrouping(d *sqltype.Descriptor) bool {
	d = d.BaseType()
	for d.IsArray() {
		d = d.Element().BaseType()
	}
	return d.ID() == sqltype.PgNodeTree
}
