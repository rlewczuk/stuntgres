package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// unknownOutput is an occurrence, not a SQL value. Assignment or ordinary
// SELECT output finalization must eliminate it before the query can execute.
type unknownOutput struct {
	expressionInfo
	raw parser.Expr
}

func (unknownOutput) Eval(*EvalContext) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unresolved query output")
}

func (b *expressionBinder) bindOutput(raw parser.Expr, contextual bool) (BoundExpr, error) {
	if contextual {
		if v, ok := raw.(parser.LiteralExpr); ok && (v.Value.Kind == parser.StringValue || v.Value.Kind == parser.NullValue) {
			return unknownOutput{expressionInfo: expressionInfo{pos: raw.SourcePosition()}, raw: raw}, nil
		}
	}
	return b.bind(raw, expressionPolicy{context: projectionContext})
}

// bindSelect is shared by public SELECT and the internal INSERT producer.
// Only the latter defers output unknowns to destination assignment.
func (b *expressionBinder) bindSelect(s *parser.Select, contextual bool) (*BoundQuery, error) {
	oldClause, oldAggregates := b.aggregateClause, b.aggregates
	defer func() { b.aggregateClause, b.aggregates = oldClause, oldAggregates }()
	b.aggregateClause, b.aggregates = "SELECT", nil
	width := len(s.Targets)
	if s.Star {
		if b.relation == nil {
			return nil, expressionError("42601", "SELECT * with no tables specified is not valid", s.StarPos)
		}
		width = len(b.relation.columns)
	}
	projection := make([]BoundExpr, width)
	var root LogicalNode = &singletonNode{}
	if b.relation != nil {
		root = &sourceNode{querySchema: b.relation.columns, ref: b.ref}
	}
	for i := range width {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		if s.Star {
			projection[i] = b.columnAt(i, s.StarPos)
		} else {
			projection[i], err = b.bindOutput(s.Targets[i], contextual)
		}
		if err != nil {
			return nil, err
		}
	}
	b.aggregateClause = "WHERE"
	if s.Where != nil {
		filter, err := b.bindBoolean(s.Where, filterContext, "WHERE")
		if err != nil {
			return nil, err
		}
		root = &filterNode{querySchema: root.Schema(), child: root, predicate: filter}
	}
	b.aggregateClause = "GROUP BY"
	keys := make([]BoundExpr, len(s.GroupBy))
	comparisons := make([]sqltype.Grouping, len(keys))
	for i, raw := range s.GroupBy {
		ordinal, ok, err := groupOrdinal(raw, width)
		if err != nil {
			return nil, err
		}
		if ok {
			keys[i] = projection[ordinal]
		} else {
			keys[i], err = b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
		}
		if unknown, ok := keys[i].(unknownOutput); ok {
			keys[i], err = b.bind(unknown.raw, expressionPolicy{context: projectionContext})
			if err != nil {
				return nil, err
			}
		}
		if call := aggregateOccurrence(keys[i]); call != nil {
			return nil, expressionError("42803", "aggregate functions are not allowed in GROUP BY", call.pos)
		}
		keys[i], err = b.finalizeProjection(keys[i])
		if err != nil {
			return nil, err
		}
		comparisons[i], err = sqltype.BindGrouping(keys[i].ResultUsage())
		if err != nil {
			return nil, err
		}
	}
	b.aggregateClause = "HAVING"
	var having BoundExpr
	if s.Having != nil {
		var err error
		having, err = b.bindBoolean(s.Having, projectionContext, "HAVING")
		if err != nil {
			return nil, err
		}
	}
	var order BoundExpr
	if s.Order != nil {
		var err error
		order, err = b.column(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: s.Order.Column, Pos: s.Order.Pos}}}, Pos: s.Order.Pos})
		if err != nil {
			return nil, err
		}
	}
	results := make([]ColumnDescriptor, width)
	regclassOutput := false
	for i, expr := range projection {
		if !contextual {
			var err error
			expr, err = b.finalizeProjection(expr)
			if err != nil {
				return nil, err
			}
			projection[i] = expr
		}
		result := ColumnDescriptor{Name: "?column?", Usage: expr.ResultUsage()}
		regclassOutput = regclassOutput || (result.Usage.Type != nil && result.Usage.Type.ID() == sqltype.Regclass)
		if !s.Star {
			result.Name, _ = expressionName(s.Targets[i], expr.ResultUsage())
		}
		if c, ok := expr.(columnExpression); ok {
			result = b.relation.columns[c.slot]
			if !s.Star {
				result.Name, _ = expressionName(s.Targets[i], expr.ResultUsage())
			}
		}
		if _, ok := expr.(aggregateExpression); ok {
			result.NullabilityKnown = true
		}
		results[i] = result
	}
	grouped := len(keys) > 0 || len(b.aggregates) > 0 || having != nil
	if grouped {
		schema := make([]ColumnDescriptor, len(keys)+len(b.aggregates))
		for i, key := range keys {
			schema[i] = ColumnDescriptor{Usage: key.ResultUsage()}
		}
		for i, provider := range b.aggregates {
			schema[len(keys)+i] = ColumnDescriptor{Usage: provider.ResultUsage()}
		}
		root = &aggregateNode{querySchema: schema, child: root, keys: keys, grouping: comparisons, providers: append([]aggregateProvider(nil), b.aggregates...)}
		for i, expr := range projection {
			var err error
			projection[i], err = b.rewriteGroup(expr, keys)
			if err != nil {
				return nil, err
			}
		}
		if having != nil {
			var err error
			having, err = b.rewriteGroup(having, keys)
			if err != nil {
				return nil, err
			}
			root = &filterNode{querySchema: root.Schema(), child: root, predicate: having}
		}
		if order != nil {
			var err error
			order, err = b.rewriteGroup(order, keys)
			if err != nil {
				return nil, err
			}
		}
	}
	if order != nil {
		slot := 0
		switch e := order.(type) {
		case columnExpression:
			slot = e.slot
		case groupExpression:
			slot = e.slot
		default:
			return nil, fmt.Errorf("stuntgres: invalid bound sort slot")
		}
		cmp, err := sqltype.BindComparison(order.ResultUsage().Type, order.ResultUsage().Type)
		if err != nil {
			return nil, err
		}
		root = &sortNode{querySchema: root.Schema(), child: root, slot: slot, comparison: cmp, desc: s.Order.Desc}
	}
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}, regclassOutput: regclassOutput}, nil
}

func (b *expressionBinder) assignOutput(expr BoundExpr, target ColumnDescriptor) (BoundExpr, error) {
	if raw, ok := expr.(unknownOutput); ok {
		return b.bind(raw.raw, expressionPolicy{context: assignmentContext, expected: target.Usage, mode: sqltype.Assignment})
	}
	if p, ok := expr.(parameterExpression); ok && p.usage.Type == nil {
		var err error
		expr, err = b.coerceParameter(p, target.Usage)
		if err != nil {
			return nil, err
		}
	}
	if expr.ResultUsage() == target.Usage {
		return expr, nil
	}
	plan, err := sqltype.PlanCoercion(expr.ResultUsage(), target.Usage, sqltype.Assignment)
	if err != nil {
		d := expressionError("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, target.Usage.Type.DiagnosticName(), expr.ResultUsage().Type.DiagnosticName()), expr.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	return coercionExpression{b.info(target.Usage, expr.SourcePosition(), expr.Properties()), expr, plan, sqltype.Assignment}, nil
}
