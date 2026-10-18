package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func setOperand(arm *projectNode, slot int) comparisonOperand {
	expression := arm.expressions[slot]
	if unknown, ok := expression.(unknownOutput); ok {
		return comparisonOperand{raw: unknown.raw}
	}
	operand := comparisonOperand{node: expression}
	if arm.syntax != nil && len(arm.syntax.Targets) == len(arm.expressions) {
		operand.raw = arm.syntax.Targets[slot]
	}
	return operand
}

func (b *expressionBinder) finalizeSetOperand(arm *projectNode, slot int, target sqltype.Usage, operation string) (expr.Expr, error) {
	original := arm.expressions[slot]
	operand := setOperand(arm, slot)
	oldFrame := b.queryFrame
	b.queryFrame = arm.frame
	defer func() { b.queryFrame = oldFrame }()
	node, err := b.coerceConditional(operand, target, operation)
	if err != nil {
		return nil, err
	}
	if unknown, ok := original.(unknownOutput); ok {
		arm.expressions[slot] = node
		arm.querySchema[slot].Usage = node.ResultUsage()
		if b.noParameters || b.materializedDefinition {
			for i, raw := range arm.syntax.Targets {
				if raw == unknown.raw {
					if err := b.captureViewLiterals(raw, node, viewTargetLiteral, i); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	} else if original.ResultUsage().Type == nil {
		arm.expressions[slot] = node
		arm.querySchema[slot].Usage = node.ResultUsage()
	}
	return node, nil
}

func (b *expressionBinder) bindSet(s *parser.Select) (*BoundQuery, error) {
	if d := b.recursiveDefinition; d != nil && d.syntax == s {
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

func (b *expressionBinder) bindSetArms(s *parser.Select, leftQuery, rightQuery *BoundQuery) (*BoundQuery, error) {
	left, right := leftQuery.Root.(*projectNode), rightQuery.Root.(*projectNode)
	operation := strings.ToUpper(s.Set.Operator)
	if len(left.Schema()) != len(right.Schema()) {
		pos := s.Set.Right.Pos
		if len(right.expressions) > 0 {
			pos = right.expressions[0].SourcePosition()
		}
		return nil, expr.ErrorAt("42601", "each "+operation+" query must have the same number of columns", pos)
	}
	width := len(left.Schema())
	n := &setNode{querySchema: make(querySchema, width), left: left, right: right, operation: strings.ToLower(operation), all: s.Set.All, leftExpressions: make([]expr.Expr, width), rightExpressions: make([]expr.Expr, width)}
	projection := make([]expr.Expr, width)
	names := make([]string, width)
	namespace := queryNamespace{frame: n.querySchema, columns: make([]queryColumn, width)}
	if !(n.operation == "union" && n.all) {
		n.grouping = make([]queryGrouping, width)
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
			plan, err := sqltype.PlanCoercion(node.ResultUsage(), common, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			coercionInfo := info
			coercionInfo.Pos = node.SourcePosition()
			coercionInfo.Property = coercionProperties(node, plan)
			expression := coercionExpression{coercionInfo, node, plan, sqltype.Implicit, false}
			if side == 0 {
				n.leftExpressions[i] = expression
			} else {
				n.rightExpressions[i] = expression
			}
		}
		n.querySchema[i] = catalog.Column{Name: left.Schema()[i].Name, Usage: common}
		names[i] = n.Schema()[i].Name
		namespace.columns[i] = queryColumn{descriptor: n.Schema()[i], slot: i, leaf: -1, physical: -1}
		projection[i] = columnExpression{Info: info, scope: 1, frame: b.frameID(), slot: i}
		regclass = regclass || common.Type.ContainsCatalogIdentifier()
		if n.grouping != nil {
			n.grouping[i], err = b.bindDistinctGrouping(projection[i])
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
	b.queryNamespace = &namespace
	order, err := b.bindSetOrder(s.Order, projection, names, right.Schema())
	if err != nil {
		return nil, err
	}
	bounds, err := b.bindQueryBounds(s.Bounds, true)
	if err != nil {
		return nil, err
	}
	var root LogicalNode = n
	root, err = b.finishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.finishTies(root)
	}
	if bounds != nil {
		bounds.querySchema, bounds.child = root.Schema(), root
		root = bounds
	}
	return &BoundQuery{Root: &projectNode{querySchema: n.querySchema, child: root, expressions: projection, frame: b.frameID(), syntax: s}, regclassOutput: regclass, visibilitySnapshot: leftQuery.visibilitySnapshot || rightQuery.visibilitySnapshot}, nil
}

func (b *expressionBinder) bindSetOrder(order *parser.Order, projection []expr.Expr, names []string, right []catalog.Column) ([]sortKey, error) {
	if order == nil {
		return nil, nil
	}
	keys := make([]sortKey, len(order.Keys))
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
		if (b.noParameters || b.materializedDefinition) && !ordinal {
			if err := b.captureViewLiterals(raw.Expr, projection[slot], viewOrderLiteral, i); err != nil {
				return nil, err
			}
		}
		keys[i] = sortKey{expression: projection[slot], position: raw.Pos.Character, outputOrdinal: slot + 1, desc: raw.Desc, nullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc, cache: -1}
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
	for _, admission := range b.queryLockAdmission.ordered {
		parent := admission.selectQuery
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
