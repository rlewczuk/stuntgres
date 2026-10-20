package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func setOperand(arm *plan.ProjectNode, slot int) comparisonOperand {
	expression := arm.Expressions[slot]
	if unknown, ok := expression.(plan.UnknownOutput); ok {
		return comparisonOperand{raw: unknown.Raw}
	}
	operand := comparisonOperand{node: expression}
	if arm.Syntax != nil && len(arm.Syntax.Targets) == len(arm.Expressions) {
		operand.raw = arm.Syntax.Targets[slot]
	}
	return operand
}

func (b *expressionBinder) finalizeSetOperand(arm *plan.ProjectNode, slot int, target sqltype.Usage, operation string) (expr.Expr, error) {
	original := arm.Expressions[slot]
	operand := setOperand(arm, slot)
	oldFrame := b.query.frame
	b.query.frame = arm.Frame
	defer func() { b.query.frame = oldFrame }()
	node, err := b.coerceConditional(operand, target, operation)
	if err != nil {
		return nil, err
	}
	if unknown, ok := original.(plan.UnknownOutput); ok {
		arm.Expressions[slot] = node
		arm.QuerySchema[slot].Usage = node.ResultUsage()
		if b.params.noParameters || b.consumer.materializedDefinition {
			for i, raw := range arm.Syntax.Targets {
				if raw == unknown.Raw {
					if err := b.captureViewLiterals(raw, node, analyze.ViewTargetLiteral, i); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	} else if original.ResultUsage().Type == nil {
		arm.Expressions[slot] = node
		arm.QuerySchema[slot].Usage = node.ResultUsage()
	}
	return node, nil
}

func (b *expressionBinder) bindSet(s *parser.Select) (*plan.Query, error) {
	if d := b.cte.recursiveDefinition; d != nil && d.Syntax == s {
		return b.bindRecursiveSet(s, d)
	}
	leftQuery, err := b.bindSelect(s.Set.Left, true)
	if err != nil {
		return nil, err
	}
	rightQuery, err := b.bindSelect(s.Set.Right, true)
	if err != nil {
		return nil, err
	}
	return b.bindSetArms(s, leftQuery, rightQuery)
}

func (b *expressionBinder) bindSetArms(s *parser.Select, leftQuery, rightQuery *plan.Query) (*plan.Query, error) {
	left, right := leftQuery.Root.(*plan.ProjectNode), rightQuery.Root.(*plan.ProjectNode)
	operation := strings.ToUpper(s.Set.Operator)
	if len(left.Schema()) != len(right.Schema()) {
		pos := s.Set.Right.Pos
		if len(right.Expressions) > 0 {
			pos = right.Expressions[0].SourcePosition()
		}
		return nil, expr.ErrorAt("42601", "each "+operation+" query must have the same number of columns", pos)
	}
	width := len(left.Schema())
	n := &plan.SetNode{QuerySchema: make(plan.QuerySchema, width), Left: left, Right: right, Operation: strings.ToLower(operation), All: s.Set.All, LeftExpressions: make([]expr.Expr, width), RightExpressions: make([]expr.Expr, width)}
	projection := make([]expr.Expr, width)
	names := make([]string, width)
	namespace := queryNamespace{frame: n.QuerySchema, columns: make([]queryColumn, width)}
	if !(n.Operation == "union" && n.All) {
		n.Grouping = make([]plan.Grouping, width)
	}
	regclass := false
	for i := range width {
		operands := []comparisonOperand{setOperand(left, i), setOperand(right, i)}
		common, err := selectCommonUsage(len(operands), func(i int) comparisonOperand { return operands[i] }, b.typ(sqltype.Text).Usage(), operation, b.diagnosticTypeName)
		if err != nil {
			return nil, err
		}
		l, err := b.finalizeSetOperand(left, i, common, operation)
		if err != nil {
			return nil, err
		}
		r, err := b.finalizeSetOperand(right, i, common, operation)
		if err != nil {
			return nil, err
		}
		position := commonOperandStart(operands[0])
		if operands[0].descriptor() == nil && operands[1].descriptor() != nil {
			position = commonOperandStart(operands[1])
		}
		info, err := b.collatedInfo(b.info(common, position, expr.Properties{RowDependent: true}), l, r)
		if err != nil {
			return nil, err
		}
		common = info.Usage
		for side, node := range [2]expr.Expr{l, r} {
			coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), common, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			coercionInfo := info
			coercionInfo.Pos = node.SourcePosition()
			coercionInfo.Property = analyze.CoercionProperties(node, coercionPlan)
			expression := plan.CoercionExpr{Info: coercionInfo, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}
			if side == 0 {
				n.LeftExpressions[i] = expression
			} else {
				n.RightExpressions[i] = expression
			}
		}
		n.QuerySchema[i] = catalog.Column{Name: left.Schema()[i].Name, Usage: common}
		names[i] = n.Schema()[i].Name
		namespace.columns[i] = queryColumn{descriptor: n.Schema()[i], slot: i, leaf: -1, physical: -1}
		projection[i] = plan.ColumnExpr{Info: info, Scope: 1, Frame: b.frameID(), Slot: i}
		regclass = regclass || common.Type.ContainsCatalogIdentifier()
		if n.Grouping != nil {
			n.Grouping[i], err = b.bindDistinctGrouping(projection[i])
			if err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
					copy := *diagnostic
					copy.Message = fmt.Sprintf("could not identify an equality operator for type %s", b.diagnosticTypeName(common.Type))
					err = &copy
				}
				return nil, err
			}
		}
	}
	b.query.namespace = &namespace
	order, err := b.bindSetOrder(s.Order, projection, names, right.Schema())
	if err != nil {
		return nil, err
	}
	bounds, err := b.bindQueryBounds(s.Bounds, true)
	if err != nil {
		return nil, err
	}
	var root plan.Node = n
	root, err = b.finishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.FinishTies(root)
	}
	if bounds != nil {
		bounds.QuerySchema, bounds.Child = root.Schema(), root
		root = bounds
	}
	return &plan.Query{Root: &plan.ProjectNode{QuerySchema: n.QuerySchema, Child: root, Expressions: projection, Frame: b.frameID(), Syntax: s}, RegclassOutput: regclass, VisibilitySnapshot: leftQuery.VisibilitySnapshot || rightQuery.VisibilitySnapshot}, nil
}

func (b *expressionBinder) bindSetOrder(order *parser.Order, projection []expr.Expr, names []string, right []catalog.Column) ([]plan.SortKey, error) {
	if order == nil {
		return nil, nil
	}
	keys := make([]plan.SortKey, len(order.Keys))
	for i, raw := range order.Keys {
		slot, ordinal, err := orderOrdinal(raw.Expr, len(projection), "ORDER BY")
		if err != nil {
			return nil, err
		}
		if !ordinal {
			slot = -1
			if column, ok := raw.Expr.(parser.ColumnExpr); ok && len(column.Name.Parts) == 1 {
				name := column.Name.Parts[0].Text
				for j, label := range names {
					if label == name {
						if slot >= 0 {
							return nil, expr.ErrorAt("42702", fmt.Sprintf(`ORDER BY "%s" is ambiguous`, name), raw.Pos)
						}
						slot = j
					}
				}
				if slot < 0 {
					for _, column := range right {
						if column.Name == name {
							d := expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), raw.Expr.SourcePosition()).(*sqlstate.Error)
							d.Detail = fmt.Sprintf(`There is a column named "%s" in table "*SELECT* 2", but it cannot be referenced from this part of the query.`, name)
							return nil, d
						}
					}
				}
			}
			if slot < 0 {
				if _, err := b.bind(raw.Expr, expressionPolicy{context: projectionContext}); err != nil {
					return nil, err
				}
				d := expr.ErrorAt("0A000", "invalid UNION/INTERSECT/EXCEPT ORDER BY clause", parser.ExpressionStart(raw.Expr)).(*sqlstate.Error)
				d.Detail = "Only result column names can be used, not expressions or functions."
				d.Hint = "Add the expression/function to every SELECT, or move the UNION into a FROM clause."
				return nil, d
			}
		}
		if (b.params.noParameters || b.consumer.materializedDefinition) && !ordinal {
			if err := b.captureViewLiterals(raw.Expr, projection[slot], analyze.ViewOrderLiteral, i); err != nil {
				return nil, err
			}
		}
		keys[i] = plan.SortKey{Expression: projection[slot], Position: raw.Pos.Character, OutputOrdinal: slot + 1, Desc: raw.Desc, NullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc, Cache: -1}
	}
	return keys, nil
}

func setOrderCollationError() error {
	d := sqlError("42P22", "could not determine which collation to use for string comparison").(*sqlstate.Error)
	d.Hint = "Use the COLLATE clause to set the collation explicitly."
	return d
}

// Only inline FROM edges propagate outer marks into set analysis. Stored views
// are bound separately and deliberately have no such admission edge.
func (b *expressionBinder) inheritedDerivedSetMode(query *parser.Select) string {
	for _, admission := range b.query.lockAdmission.Ordered {
		parent := admission.SelectQuery
		var alias string
		found := false
		var visit func(parser.FromItem, string)
		visit = func(item parser.FromItem, name string) {
			switch n := item.(type) {
			case *parser.DerivedQuery:
				if n.Query == query {
					found, alias = true, name
				}
			case *parser.AliasReference:
				visit(n.Input, n.Name.Text)
			case *parser.JoinReference:
				visit(n.Left, "")
				visit(n.Right, "")
			}
		}
		visit(parent.From, "")
		if !found {
			continue
		}
		for _, clause := range parent.Locking {
			if len(clause.Targets) == 0 {
				return clause.Mode
			}
			for _, target := range clause.Targets {
				if len(target.Parts) == 1 && target.Parts[0].Text == alias {
					return clause.Mode
				}
			}
		}
		if mode := b.inheritedDerivedSetMode(parent); mode != "" {
			return mode
		}
	}
	return ""
}
