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
	if s.Values != nil {
		if s.Locking != nil {
			return nil, sqlError("0A000", s.Locking.Mode+" cannot be applied to VALUES")
		}
		return b.bindValues(s.Values, false)
	}
	oldClause, oldAggregates := b.aggregateClause, b.aggregates
	defer func() { b.aggregateClause, b.aggregates = oldClause, oldAggregates }()
	root, namespace, err := b.bindFrom(s.From)
	if err != nil {
		return nil, err
	}
	b.queryNamespace = nil
	if s.From != nil {
		b.queryNamespace = &namespace
	}
	b.aggregateClause, b.aggregates = "SELECT", nil
	var projection []BoundExpr
	var targetNames []string
	var literalTargets []int
	// Syntax-only cast candidates are admitted only by this coherent binder.
	// INSERT producers and previously selected singleton families keep their
	// existing rules; every new standalone target needs its own type witness.
	typeSingleton := !contextual && s.From == nil && !s.IsSingleton() && s.IsTypeSingleton()
	for target, raw := range s.Targets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := raw.(parser.StarExpr); ok {
			columns := namespace.columns
			if len(star.Qualifier.Parts) != 0 {
				q, err := b.queryQualifier(star.Qualifier, star.Pos)
				if err != nil {
					return nil, err
				}
				columns = q.columns
			} else if s.From == nil {
				return nil, expressionError("42601", "SELECT * with no tables specified is not valid", star.Pos)
			}
			if b.noParameters {
				if b.viewStars == nil {
					b.viewStars = make(map[int][]int)
				}
				b.viewStars[target] = nil
			}
			for _, c := range columns {
				projection = append(projection, b.queryColumnAt(c, star.Pos))
				targetNames = append(targetNames, c.descriptor.Name)
				if b.noParameters {
					b.viewStars[target] = append(b.viewStars[target], c.slot)
					literalTargets = append(literalTargets, -1)
				}
			}
		} else {
			typeWitnesses := b.typeSingletons
			expr, err := b.bindOutput(raw, contextual)
			if err != nil {
				return nil, err
			}
			if typeSingleton && b.typeSingletons == typeWitnesses {
				return nil, expressionError("0A000", "SELECT without FROM is unsupported", s.Pos)
			}
			projection = append(projection, expr)
			if b.noParameters {
				literalTargets = append(literalTargets, target)
			}
			name, _ := expressionName(raw, expr.ResultUsage())
			if target < len(s.Labels) && s.Labels[target].Text != "" {
				name = s.Labels[target].Text
			}
			targetNames = append(targetNames, name)
		}
	}
	width := len(projection)
	b.aggregateClause = "WHERE"
	if s.Where != nil {
		filter, err := b.bindBoolean(s.Where, filterContext, "WHERE")
		if err != nil {
			return nil, err
		}
		if b.noParameters {
			if err := b.captureViewLiterals(s.Where, filter, viewWhereLiteral, 0); err != nil {
				return nil, err
			}
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
		if b.noParameters && !ok {
			if err := b.captureViewLiterals(raw, keys[i], viewGroupLiteral, i); err != nil {
				return nil, err
			}
		}
		comparisons[i], err = sqltype.BindGrouping(keys[i].ResultUsage())
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && keys[i].ResultUsage().Type.BaseType().ID() == sqltype.JSON {
				copy := *diagnostic
				if pos := raw.SourcePosition(); pos.Character > 0 {
					copy.Position = fmt.Sprint(pos.Character)
				}
				return nil, &copy
			}
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
		if b.noParameters {
			if err := b.captureViewLiterals(s.Having, having, viewHavingLiteral, 0); err != nil {
				return nil, err
			}
		}
	}
	if s.Distinct {
		for i, expression := range projection {
			projection[i], err = b.orderOutput(expression)
			if err != nil {
				return nil, err
			}
		}
	}
	b.aggregateClause = "ORDER BY"
	order, err := b.bindOrder(s.Order, projection, targetNames)
	if err != nil {
		return nil, err
	}
	if b.noParameters && s.Order != nil {
		frozen := *s.Order
		frozen.Keys = append([]parser.OrderKey(nil), s.Order.Keys...)
		for i, key := range frozen.Keys {
			if ordinal := order[i].outputOrdinal; ordinal != 0 {
				// Freeze the exact resolved output occurrence, independently of
				// equivalent expression syntax or a view column-name override.
				frozen.Keys[i].Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: key.Pos}}
				continue
			}
			if err := b.captureViewLiterals(key.Expr, order[i].expression, viewOrderLiteral, i); err != nil {
				return nil, err
			}
		}
		s.Order = &frozen
	}
	var distinctOrder []int
	if s.Distinct {
		distinctOrder = make([]int, len(order))
		for i, key := range order {
			slot := key.outputOrdinal - 1
			if slot < 0 {
				for j, expression := range projection {
					if b.sameGroupExpression(key.expression, expression) {
						slot = j
						break
					}
				}
			}
			if slot < 0 {
				return nil, expressionError("42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", parser.ExpressionStart(s.Order.Keys[i].Expr))
			}
			distinctOrder[i] = slot
		}
	}
	bounds, err := b.bindQueryBounds(s.Bounds, true)
	if err != nil {
		return nil, err
	}
	results := make([]ColumnDescriptor, width)
	regclassOutput := false
	for i, expr := range projection {
		if !contextual || s.Distinct {
			var err error
			expr, err = b.finalizeProjection(expr)
			if err != nil {
				return nil, err
			}
			projection[i] = expr
		}
		if b.noParameters && literalTargets[i] >= 0 {
			target := literalTargets[i]
			if err := b.captureViewLiterals(s.Targets[target], expr, viewTargetLiteral, target); err != nil {
				return nil, err
			}
		}
		result := ColumnDescriptor{Name: "?column?", Usage: expr.ResultUsage()}
		regclassOutput = regclassOutput || result.Usage.Type.ContainsRegclass()
		result.Name = targetNames[i]
		if c, ok := expr.(columnExpression); ok {
			result = namespace.frame[c.slot]
			result.Name = targetNames[i]
		}
		if aggregate, ok := expr.(aggregateExpression); ok {
			result.NullabilityKnown = true
			result.Nullable = b.aggregates[aggregate.slot].operation != aggregateCount
		}
		if _, ok := expr.(currentSchemasExpression); ok {
			result.Nullable, result.NullabilityKnown = true, true
		}
		if identity, ok := expr.(identityExpression); ok && identity.kind == identityCurrentSchema {
			result.Nullable, result.NullabilityKnown = true, true
		}
		if _, ok := expr.(deparseExpression); ok {
			result.Nullable, result.NullabilityKnown = true, true
		}
		results[i] = result
	}
	var distinctFamilies []sqltype.Grouping
	if s.Distinct {
		if width == 0 {
			return nil, sqlError("42601", "SELECT DISTINCT must have at least one column")
		}
		distinctFamilies = make([]sqltype.Grouping, width)
		for i, expression := range projection {
			distinctFamilies[i], err = bindDistinctGrouping(expression)
			if err != nil {
				return nil, err
			}
		}
	}
	grouped := len(keys) > 0 || len(b.aggregates) > 0 || having != nil
	if s.Locking != nil {
		suffix := ""
		switch {
		case s.Distinct:
			suffix = "DISTINCT clause"
		case len(s.GroupBy) != 0:
			suffix = "GROUP BY clause"
		case s.Having != nil:
			suffix = "HAVING clause"
		case len(b.aggregates) != 0:
			suffix = "aggregate functions"
		}
		if suffix != "" {
			return nil, sqlError("0A000", s.Locking.Mode+" is not allowed with "+suffix)
		}
	}
	if grouped {
		schema := make([]ColumnDescriptor, len(keys)+len(b.aggregates))
		for i, key := range keys {
			schema[i] = ColumnDescriptor{Usage: key.ResultUsage()}
		}
		for i, call := range b.aggregates {
			schema[len(keys)+i] = ColumnDescriptor{Usage: call.provider.ResultUsage()}
		}
		root = &aggregateNode{querySchema: schema, child: root, keys: keys, grouping: comparisons, calls: append([]aggregateCall(nil), b.aggregates...)}
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
		for i := range order {
			order[i].expression, err = b.rewriteGroup(order[i].expression, keys)
			if err != nil {
				return nil, err
			}
		}
	}
	if s.Distinct {
		root = &distinctNode{querySchema: results, child: root, expressions: projection, grouping: distinctFamilies}
		slots := make([]BoundExpr, len(projection))
		for i, expression := range projection {
			slots[i] = columnExpression{expressionInfo: boundExpressionInfo(expression), scope: 1, frame: 1, slot: i}
		}
		for i := range order {
			order[i].expression = slots[distinctOrder[i]]
		}
		projection = slots
	}
	root, err = b.finishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		root = b.scheduleCountProjection(root, projection)
	}
	if s.Locking != nil {
		root = &rowLockNode{querySchema: root.Schema(), child: root, mode: queryRowMode(s.Locking.Mode)}
	}
	if bounds != nil {
		bounds.querySchema, bounds.child = root.Schema(), root
		root = bounds
	}
	if contextual && s.Distinct {
		// INSERT assignment mutates only the final output descriptors, never the
		// source-typed descriptors retained by DISTINCT and its ordering/bounds.
		results = append([]ColumnDescriptor(nil), results...)
	}
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}, regclassOutput: regclassOutput, visibilitySnapshot: b.visibilitySnapshot}, nil
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
		d := expressionError("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, b.diagnosticTypeName(target.Usage.Type), b.diagnosticTypeName(expr.ResultUsage().Type)), expr.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	return coercionExpression{b.info(target.Usage, expr.SourcePosition(), coercionProperties(expr, plan)), expr, plan, sqltype.Assignment}, nil
}
