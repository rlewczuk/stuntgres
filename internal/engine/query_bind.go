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

// bindSelect is shared by public SELECT, set operands and INSERT producers.
// Contextual outputs defer unknown fallback until their consuming boundary.
func (b *expressionBinder) bindSelect(s *parser.Select, contextual bool) (*BoundQuery, error) {
	return b.bindSelectScoped(s, contextual, false)
}
func (b *expressionBinder) bindSelectScoped(s *parser.Select, contextual, disallowParent bool) (*BoundQuery, error) {
	if s.Into != nil {
		return nil, insertError("42601", "SELECT ... INTO is not allowed here", s.Into.Parts[0].Pos)
	}
	if b.queryLockAdmission == nil || b.queryLockAdmission.queries == nil {
		admission, _, _, err := newQueryLockAdmission(s)
		if err != nil {
			return nil, err
		}
		b.queryLockAdmission = &admission
	}
	restore := b.enterQuery(disallowParent)
	defer restore()
	plainDistinct := s.Distinct != nil && len(s.Distinct.On) == 0
	if s.Set != nil {
		if mode := firstSetRowClause(s); mode != "" {
			return nil, sqlError("0A000", mode+" is not allowed with UNION/INTERSECT/EXCEPT")
		}
		if mode := b.inheritedDerivedSetMode(s); mode != "" {
			return nil, sqlError("0A000", mode+" is not allowed with UNION/INTERSECT/EXCEPT")
		}
		return b.bindSet(s)
	}
	if len(s.Locking) != 0 {
		b.queryRowDiagnosticMode = queryRowMode(s.Locking[0].Mode)
	}
	if disallowParent {
		selected, _ := queryLockTargets(s.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}})
		index := 0
		_ = parser.VisitQuerySources(s, nil, func(table *parser.TableReference, _ *parser.Select) error {
			if index < len(selected) && selected[index].mode != 0 {
				b.queryRowDiagnosticMode = strongerQueryRowMode(b.queryRowDiagnosticMode, queryRowDiagnostic(b.queryLockAdmission.tableMark(table)))
			}
			index++
			return nil
		})
	}
	if s.Values != nil {
		query, err := b.bindValues(s.Values, false)
		if err != nil {
			return nil, err
		}
		// VALUES cell analysis precedes row-lock legality; retain the wrapper
		// through INSERT and derived conversion so this owner sees every clause.
		if len(s.Locking) != 0 {
			return nil, sqlError("0A000", s.Locking[0].Mode+" cannot be applied to VALUES")
		}
		project := query.Root.(*projectNode)
		project.frame, project.syntax = b.frameID(), s
		return query, err
	}
	root, namespace, err := b.bindFrom(s.From)
	if err != nil {
		return nil, err
	}
	b.queryNamespace = &namespace
	b.aggregateClause, b.aggregates = "SELECT", nil
	b.windowRaw = s.Windows
	var projection []BoundExpr
	var targetNames []string
	var literalTargets []int
	for target, raw := range s.Targets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := raw.(parser.StarExpr); ok {
			columns := namespace.columns
			frame := b.frameID()
			starPath := viewLiteralPath{query: frame, clause: viewTargetLiteral, target: target}
			var returningScope *queryBindingScope
			newValues := false
			if len(star.Qualifier.Parts) != 0 {
				returningScope, newValues = b.outerReturningScope(star.Qualifier)
				if returningScope != nil {
					columns = returningScope.namespace.columns[:len(returningScope.relation.columns)]
					frame = returningScope.frame
				} else {
					q, err := b.queryQualifier(star.Qualifier, star.Pos)
					if err != nil {
						return nil, err
					}
					columns, frame = q.columns, b.resolvedQueryFrame
				}
			} else if s.From == nil {
				return nil, expressionError("42601", "SELECT * with no tables specified is not valid", star.Pos)
			}
			if b.noParameters {
				if b.viewStars == nil {
					b.viewStars = make(map[viewLiteralPath][]viewColumn)
				}
				b.viewStars[starPath] = nil
			}
			for _, c := range columns {
				var expr BoundExpr
				if returningScope != nil {
					expr = b.outerReturningColumnAt(returningScope, c.physical, star.Pos, newValues)
				} else {
					expr = b.queryColumnInFrame(c, star.Pos, frame)
				}
				projection = append(projection, expr)
				targetNames = append(targetNames, c.descriptor.Name)
				if b.noParameters {
					b.viewStars[starPath] = append(b.viewStars[starPath], viewColumn{frame: frame, slot: c.slot})
					literalTargets = append(literalTargets, -1)
				}
			}
		} else {
			expr, err := b.bindOutput(raw, contextual && !plainDistinct)
			if err != nil {
				return nil, err
			}
			projection = append(projection, expr)
			if b.noParameters {
				literalTargets = append(literalTargets, target)
			}
			name, _ := expressionName(raw, expr.ResultUsage(), expr)
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
		if !contextual && !b.noParameters && len(s.Locking) == 0 {
			if source, ok := root.(*sourceNode); ok && len(b.querySources) == 1 && b.querySources[0].mark.mode == 0 {
				b.querySources[0].partialCandidate = b.partialIndexCandidate(source.ref, filter)
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
		if call := aggregateOccurrence(keys[i]); call != nil && (call.frame == 0 || call.frame == b.frameID()) {
			return nil, expressionError("42803", "aggregate functions are not allowed in GROUP BY", call.pos)
		}
		if call := windowOccurrence(keys[i]); call != nil && call.frame == b.frameID() {
			return nil, expressionError("42P20", "window functions are not allowed in GROUP BY", call.pos)
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
	if plainDistinct {
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
	if s.Distinct != nil && len(s.Distinct.On) != 0 {
		rawKeys := make([]parser.OrderKey, len(s.Distinct.On))
		for i, raw := range s.Distinct.On {
			rawKeys[i] = parser.OrderKey{Expr: raw, Pos: raw.SourcePosition()}
		}
		distinct, err := b.bindOrder(&parser.Order{Keys: rawKeys}, projection, targetNames)
		if err != nil {
			return nil, err
		}
		matched := make([]bool, len(distinct))
		unmatchedOrder := false
		for _, key := range order {
			found := false
			for i, on := range distinct {
				if b.sameGroupExpression(key.expression, on.expression) {
					if unmatchedOrder {
						return nil, expressionError("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions", s.Distinct.On[i].SourcePosition())
					}
					matched[i], found = true, true
				}
			}
			if !found {
				unmatchedOrder = true
			}
		}
		if unmatchedOrder {
			for i, found := range matched {
				if !found {
					return nil, expressionError("42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions", s.Distinct.On[i].SourcePosition())
				}
			}
		}
	}
	var distinctOrder []int
	if plainDistinct {
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
	if err := b.resolveWindows(); err != nil {
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
	bounds, err := b.bindQueryBounds(s.Bounds, true)
	if err != nil {
		return nil, err
	}
	results := make([]ColumnDescriptor, width)
	regclassOutput := false
	for i, expr := range projection {
		if !contextual || plainDistinct {
			var err error
			expr, err = b.finalizeProjection(expr)
			if err != nil {
				return nil, err
			}
			projection[i] = expr
		}
		if b.noParameters && literalTargets[i] >= 0 && expr.ResultUsage().Type != nil {
			target := literalTargets[i]
			if err := b.captureViewLiterals(s.Targets[target], expr, viewTargetLiteral, target); err != nil {
				return nil, err
			}
		}
		result := ColumnDescriptor{Name: "?column?", Usage: expr.ResultUsage()}
		regclassOutput = regclassOutput || result.Usage.Type.ContainsCatalogIdentifier()
		result.Name = targetNames[i]
		if c, ok := expr.(columnExpression); ok && c.frame == b.frameID() {
			result = namespace.frame[c.slot]
			result.Name = targetNames[i]
		}
		if aggregate, ok := expr.(aggregateExpression); ok && aggregate.frame == b.frameID() {
			result.NullabilityKnown = true
			result.Nullable = b.aggregates[aggregate.slot].operation != aggregateCount
		}
		if _, ok := expr.(*windowExpression); ok {
			result.Nullable, result.NullabilityKnown = false, true
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
		if _, ok := expr.(indexPropertyExpression); ok {
			result.Nullable, result.NullabilityKnown = true, true
		}
		results[i] = result
	}
	var distinctFamilies []sqltype.Grouping
	if plainDistinct {
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
	if b.queryRowDiagnosticMode != 0 {
		suffix := ""
		switch {
		case s.Distinct != nil:
			suffix = "DISTINCT clause"
		case len(s.GroupBy) != 0:
			suffix = "GROUP BY clause"
		case s.Having != nil:
			suffix = "HAVING clause"
		case len(b.aggregates) != 0:
			suffix = "aggregate functions"
		case len(b.windowCalls) != 0:
			suffix = "window functions"
		}
		if suffix != "" {
			mode := queryRowClause(b.queryRowDiagnosticMode)
			if len(s.Locking) != 0 {
				mode = s.Locking[0].Mode
			}
			return nil, sqlError("0A000", mode+" is not allowed with "+suffix)
		}
	}
	if s.Distinct != nil && len(s.Distinct.On) != 0 {
		return nil, sqlError("0A000", "SELECT DISTINCT ON is unsupported")
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
	root, err = b.finishWindows(root, keys, grouped)
	if err != nil {
		return nil, err
	}
	if plainDistinct {
		root = &distinctNode{querySchema: results, child: root, expressions: projection, grouping: distinctFamilies}
		slots := make([]BoundExpr, len(projection))
		for i, expression := range projection {
			slots[i] = columnExpression{expressionInfo: boundExpressionInfo(expression), scope: 1, frame: b.frameID(), slot: i}
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
	root = b.scheduleQueryProjection(root, projection, bounds != nil)
	if len(s.Locking) != 0 {
		admission := b.queryLockAdmission.queries[s]
		var local []queryRowMark
		var err error
		if admission != nil {
			local, err = admission.marks, admission.err
		} else {
			local, err = queryLockTargets(s.From, s.Locking)
		}
		if err != nil {
			return nil, err
		}
		indices := make(map[*parser.TableReference]int)
		_ = parser.VisitQuerySources(s, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		marks := make([]queryRowMark, len(b.querySources))
		for i, source := range b.querySources {
			if index, ok := indices[source.table]; ok && index < len(local) {
				marks[i] = local[index]
			}
		}
		root = &rowLockNode{querySchema: root.Schema(), child: root, marks: marks, firstMode: queryRowMode(s.Locking[0].Mode)}
	}
	if bounds != nil {
		bounds.querySchema, bounds.child = root.Schema(), root
		root = bounds
	}
	if contextual && plainDistinct {
		// INSERT assignment mutates only the final output descriptors, never the
		// source-typed descriptors retained by DISTINCT and its ordering/bounds.
		results = append([]ColumnDescriptor(nil), results...)
	}
	var analysis []BoundExpr
	for _, definition := range b.windowRaw {
		spec := b.windowDefinitions[definition.Name.Text]
		analysis = append(analysis, spec.partition...)
		for _, key := range spec.order {
			analysis = append(analysis, key.expression)
		}
	}
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection, analysisExpressions: analysis, frame: b.frameID(), syntax: s}, regclassOutput: regclassOutput, visibilitySnapshot: b.visibilitySnapshot}, nil
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
	if plan.RequiresCatalogOutput() {
		b.regclassConcat = true
	}
	return coercionExpression{b.info(target.Usage, expr.SourcePosition(), coercionProperties(expr, plan)), expr, plan, sqltype.Assignment}, nil
}
