package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSubquery(raw parser.SubqueryExpr) (expr.Expr, error) {
	if b.consumer.defaultConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in DEFAULT expression", raw.Pos)
	}
	if b.consumer.checkConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in check constraint", raw.Pos)
	}
	if b.consumer.indexPredicateConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", raw.Pos)
	}
	if b.consumer.indexConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", raw.Pos)
	}
	if b.target.generatedOwner != nil {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in column generation expression", raw.Pos)
	}
	b.query.subqueryDemand = true
	var left []comparisonOperand
	var leftExpansions map[int]plan.CompositeExpansionBinding
	var leftConstructor expr.Expr
	var erasedLeftType sqltype.ID
	if raw.Left != nil {
		var err error
		if row, ok := raw.Left.(parser.RowExpr); ok {
			left, leftExpansions, err = b.rowComparisonOperands(row)
		} else {
			var operand comparisonOperand
			operand, err = b.comparisonOperand(raw.Left)
			if constructor, ok := operand.node.(plan.RowExpr); ok && constructor.Raw != nil && !constructor.Usage.Type.IsDomain() {
				// A cast to a named composite can still analyze to a ROW
				// constructor. Quantified subqueries compare its fields;
				// a domain coercion or scalar subquery remains one value.
				left = make([]comparisonOperand, len(constructor.Fields))
				for i, field := range constructor.Fields {
					left[i] = comparisonOperand{node: field}
				}
				leftExpansions = constructor.Expansions
				if b.params.noParameters || b.consumer.materializedDefinition {
					erasedLeftType = constructor.Usage.Type.ID()
					leftConstructor, err = b.normalizeQuantifiedConstructor(raw.Left, constructor)
					if err != nil {
						return nil, err
					}
				}
			} else {
				left = []comparisonOperand{operand}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	query, err := b.bindSelect(raw.Query, false)
	if err != nil {
		return nil, err
	}
	project := query.Root.(*plan.ProjectNode)
	width := len(project.Schema())
	usage := b.typ(sqltype.Bool).Usage()
	if raw.Kind == parser.SubqueryScalar {
		if width != 1 {
			return nil, expr.ErrorAt("42601", "subquery must return only one column", raw.Pos)
		}
		usage = project.Schema()[0].Usage
	} else if raw.Kind != parser.SubqueryExists {
		if width < len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too few columns", raw.Pos)
		}
		if width > len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too many columns", raw.Pos)
		}
		if width == 0 {
			return nil, expr.ErrorAt("0A000", "cannot compare rows of zero length", raw.Pos)
		}
	}
	presenceOnly := raw.Kind == parser.SubqueryExists && canSimplifyExists(query)
	if presenceOnly {
		query, err = presenceQueryPlan(query)
		if err != nil {
			return nil, err
		}
		project = query.Root.(*plan.ProjectNode)
	}
	properties := queryDemandProperties(project, presenceOnly)
	properties.StageDependent = true
	e := plan.SubqueryExpr{Info: b.info(usage, raw.Pos, properties), Kind: raw.Kind, QueryPlan: query, Negate: raw.Negate, PresenceOnly: presenceOnly}
	e.LeftExpansions = leftExpansions
	e.LeftConstructor, e.ErasedLeftType = leftConstructor, erasedLeftType
	if raw.Kind == parser.SubqueryScalar || raw.Kind == parser.SubqueryExists {
		return e, nil
	}
	e.Operator = raw.Operator.Parts[len(raw.Operator.Parts)-1].Text
	operator := sqltype.CompareEqual
	switch e.Operator {
	case "=":
	case "<>":
		operator = sqltype.CompareNotEqual
	case "<":
		operator = sqltype.CompareLess
	case "<=":
		operator = sqltype.CompareLessEqual
	case ">":
		operator = sqltype.CompareGreater
	case ">=":
		operator = sqltype.CompareGreaterEqual
	default:
		return nil, fmt.Errorf("stuntgres: invalid subquery comparison operator")
	}
	e.Comparisons = make([]plan.ComparisonExpr, width)
	if width > 1 && operator > sqltype.CompareNotEqual {
		e.Ordering = make([]sqltype.Comparison, width)
	}
	for i, column := range project.Schema() {
		right := plan.ColumnExpr{Info: b.info(column.Usage, raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: project.Frame, Slot: i}
		bound, err := b.bindComparisonOperands(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, operator, left[i], comparisonOperand{node: right})
		if err != nil {
			return nil, err
		}
		e.Comparisons[i] = bound.(plan.ComparisonExpr)
		e.Property = expr.MergeProperties(e.Property, e.Comparisons[i].Left.Properties())
		if len(e.Ordering) != 0 {
			ref, conflict := e.Comparisons[i].Predicate.InputCollation()
			e.Ordering[i], err = sqltype.BindComparison(e.Comparisons[i].Left.ResultUsage().Type, e.Comparisons[i].Right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := b.checkRowComparisonInterpretation(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, e.Comparisons); err != nil {
		return nil, err
	}
	if raw.Kind == parser.SubqueryAny && operator == sqltype.CompareEqual && !properties.RowDependent {
		families := make([]sqltype.Grouping, width)
		for i, comparison := range e.Comparisons {
			families[i], err = comparison.Predicate.EqualityGrouping()
			if err != nil {
				families = nil
				break
			}
		}
		e.Membership = families
	}
	return e, nil
}

// Full analysis precedes this demand decision. A represented OFFSET, actual local
// aggregate, HAVING or rowmark retains ordinary tuple projection.
// Only a proven positive/NULL literal LIMIT is removable; parameters are not
// evaluated during binding. Retain all other plans conservatively.
func canSimplifyExists(query *plan.Query) bool {
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
func presenceQueryPlan(query *plan.Query) (*plan.Query, error) {
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
	child := presenceQueryInput(project.Child)
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

func presenceQueryInput(node plan.Node) plan.Node {
	switch n := node.(type) {
	case *plan.EvaluationNode:
		return presenceQueryInput(n.Child)
	case *plan.SortNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	case *plan.FilterNode:
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.AggregateNode:
		if n.DiscardableGrouping() {
			copy := *n
			copy.Child = presenceQueryInput(n.Child)
			copy.PresenceOnly = true
			return &copy
		}
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.RowLockNode:
		child := presenceQueryInput(n.Child)
		if child == n.Child {
			return node
		}
		copy := *n
		copy.Child = child
		return &copy
	case *plan.LimitNode:
		copy := *n
		copy.Child = presenceQueryInput(n.Child)
		copy.PresenceOnly = true
		return &copy
	default:
		return node
	}
}
