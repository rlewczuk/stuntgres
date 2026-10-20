package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindOutput(raw parser.Expr, contextual bool) (expr.Expr, error) {
	if contextual {
		if v, ok := raw.(parser.LiteralExpr); ok && (v.Value.Kind == parser.StringValue || v.Value.Kind == parser.NullValue) {
			return unknownOutput{Info: expr.Info{Pos: raw.SourcePosition()}, raw: raw}, nil
		}
	}
	return b.bind(raw, expressionPolicy{context: projectionContext})
}

// bindSelect is shared by public SELECT, set operands and INSERT producers.
// Contextual outputs defer unknown fallback until their consuming boundary.
func (b *expressionBinder) bindSelect(s *parser.Select, contextual bool) (*BoundQuery, error) {
	return b.bindSelectScoped(s, contextual, false)
}
func (b *expressionBinder) bindSelectScoped(s *parser.Select, contextual, disallowParent bool) (query *BoundQuery, failure error) {
	entry, scanStart := b.cte.analysisDepth == 0, b.cte.scanOrdinal
	defer func() {
		if entry && failure == nil && query != nil && b.cte.scanOrdinal != scanStart {
			b.cte.executables = append(b.cte.executables, query)
		}
	}()
	if s.Into != nil {
		return nil, insertError("42601", "SELECT ... INTO is not allowed here", s.Into.Parts[0].Pos)
	}
	if b.query.lockAdmission == nil || b.query.lockAdmission.queries == nil {
		admission, _, _, err := newQueryLockAdmission(s)
		if err != nil {
			return nil, err
		}
		b.query.lockAdmission = &admission
	}
	restore := b.enterQuery(disallowParent)
	defer restore()
	defer func() {
		if failure == nil && query != nil && (b.params.noParameters || b.consumer.materializedDefinition) {
			b.normalizeStoredQuerySyntax(s)
		}
	}()
	oldCTEs := len(b.cte.bindings)
	defer func() { b.cte.bindings = b.cte.bindings[:oldCTEs] }()
	definitions, err := b.bindCTEDefinitions(s.With, s.WithRecursive)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failure != nil || query == nil || len(definitions) == 0 {
			return
		}
		project := query.Root.(*projectNode)
		project.cteOwner = !parser.HasModifyingCTE(s)
		if parser.HasModifyingCTE(s) {
			b.cte.statementCTEs = definitions
		}
		for _, definition := range definitions {
			if definition.query != nil {
				project.analysisExpressions = append(project.analysisExpressions, subqueryExpression{Info: b.info(b.typ(sqltype.Bool).Usage(), definition.position, expr.Properties{}), queryPlan: definition.query, cteDefinition: true})
			}
		}
	}()
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
		b.query.rowDiagnosticMode = queryRowMode(s.Locking[0].Mode)
	}
	if disallowParent {
		selected, _ := queryLockTargets(s.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}}, b.query.lockAdmission.virtualCTE)
		index := 0
		_ = parser.VisitQuerySources(&parser.Select{From: s.From}, nil, func(table *parser.TableReference, _ *parser.Select) error {
			if index < len(selected) && selected[index].mode != 0 {
				b.query.rowDiagnosticMode = strongerQueryRowMode(b.query.rowDiagnosticMode, queryRowDiagnostic(b.query.lockAdmission.tableMark(table)))
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
	b.query.namespace = &namespace
	b.clause.aggregateClause, b.grouping.aggregates = "SELECT", nil
	b.grouping.windowRaw = s.Windows
	var projection []expr.Expr
	var targetNames []string
	var literalTargets []int
	for target, raw := range s.Targets {
		if err := b.ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := raw.(parser.StarExpr); ok {
			if b.target.ruleNamespace != nil && len(star.Qualifier.Parts) == 1 && (star.Qualifier.Parts[0].Text == "old" || star.Qualifier.Parts[0].Text == "new") {
				path := viewLiteralPath{query: b.frameID(), clause: viewTargetLiteral, target: target}
				if b.capture.stars == nil {
					b.capture.stars = make(map[viewLiteralPath][]viewColumn)
				}
				b.capture.stars[path] = nil
				for _, column := range b.target.ruleNamespace.relation.Columns {
					name := star.Qualifier
					name.Parts = []parser.Identifier{star.Qualifier.Parts[0], {Text: column.Name}}
					expression, _, err := b.ruleColumn(parser.ColumnExpr{Name: name, Pos: star.Pos})
					if err != nil {
						return nil, err
					}
					pseudo := expression.(columnExpression)
					projection = append(projection, expression)
					targetNames = append(targetNames, column.Name)
					literalTargets = append(literalTargets, -1)
					b.capture.stars[path] = append(b.capture.stars[path], viewColumn{ruleRef: pseudo.origin, ruleSide: pseudo.frame})
				}
				continue
			}
			columns := namespace.columns
			frame := b.frameID()
			starPath := viewLiteralPath{query: frame, clause: viewTargetLiteral, target: target}
			var returningScope *queryBindingScope
			newValues := false
			var recordQualifier *queryQualifier
			if len(star.Qualifier.Parts) != 0 {
				returningScope, newValues = b.outerReturningScope(star.Qualifier)
				if returningScope != nil {
					columns = returningScope.namespace.columns[:len(returningScope.relation.Columns)]
					frame = returningScope.frame
				} else {
					q, err := b.queryQualifier(star.Qualifier, star.Pos)
					if err != nil {
						return nil, err
					}
					columns, frame = q.columns, b.query.resolvedQueryFrame
					recordQualifier = q
				}
			} else if s.From == nil {
				return nil, expr.ErrorAt("42601", "SELECT * with no tables specified is not valid", star.Pos)
			}
			if returningScope == nil {
				frozenColumns, err := b.jsonbRecordStarColumns(columns, frame, star, recordQualifier)
				if err != nil {
					return nil, err
				}
				columns = frozenColumns
			}
			if b.params.noParameters || b.consumer.materializedDefinition {
				if b.capture.stars == nil {
					b.capture.stars = make(map[viewLiteralPath][]viewColumn)
				}
				b.capture.stars[starPath] = nil
			}
			for _, c := range columns {
				if c.hideStar {
					continue
				}
				var expr expr.Expr
				if returningScope != nil {
					expr = b.outerReturningColumnAt(returningScope, c.physical, star.Pos, newValues)
				} else {
					expr = b.queryColumnInFrame(c, star.Pos, frame)
				}
				projection = append(projection, expr)
				targetNames = append(targetNames, c.descriptor.Name)
				if b.params.noParameters || b.consumer.materializedDefinition {
					b.capture.stars[starPath] = append(b.capture.stars[starPath], viewColumn{frame: frame, slot: c.slot})
					literalTargets = append(literalTargets, -1)
				}
			}
		} else if field, ok := raw.(parser.FieldExpr); ok && field.Expansion {
			expansion, err := b.expandComposite(field)
			if err != nil {
				return nil, err
			}
			projection = append(projection, expansion.fields...)
			targetNames = append(targetNames, expansion.names...)
			if b.params.noParameters || b.consumer.materializedDefinition {
				path := viewLiteralPath{query: b.frameID(), clause: viewTargetLiteral, target: target}
				ordinal := 0
				if err := b.captureCompositeExpansion(field, expansion, path, &ordinal); err != nil {
					return nil, err
				}
				for range expansion.fields {
					literalTargets = append(literalTargets, -1)
				}
			}
		} else {
			expr, err := b.bindOutput(raw, contextual && !plainDistinct)
			if err != nil {
				return nil, err
			}
			projection = append(projection, expr)
			if b.params.noParameters || b.consumer.materializedDefinition {
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
	b.clause.aggregateClause = "WHERE"
	if s.Where != nil {
		filter, err := b.bindBoolean(s.Where, filterContext, "WHERE")
		if err != nil {
			return nil, err
		}
		if b.params.noParameters || b.consumer.materializedDefinition {
			if err := b.captureViewLiterals(s.Where, filter, viewWhereLiteral, 0); err != nil {
				return nil, err
			}
		}
		if !contextual && !b.params.noParameters && !b.consumer.materializedDefinition && len(s.Locking) == 0 {
			if source, ok := root.(*sourceNode); ok && len(b.query.sources) == 1 && b.query.sources[0].mark.mode == 0 {
				b.query.sources[0].partialCandidate = b.query.partialIndexCandidate(source.ref, filter)
			}
		}
		root = &filterNode{querySchema: root.Schema(), child: root, predicate: filter}
	}
	b.clause.aggregateClause = "GROUP BY"
	keys, comparisons, sets, err := b.bindGroupClause(s.GroupBy, projection, targetNames)
	if err != nil {
		return nil, err
	}
	b.grouping.groupSets = sets
	b.clause.aggregateClause = "HAVING"
	var having expr.Expr
	if s.Having != nil {
		var err error
		having, err = b.bindBoolean(s.Having, projectionContext, "HAVING")
		if err != nil {
			return nil, err
		}
		if b.params.noParameters || b.consumer.materializedDefinition {
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
	b.clause.aggregateClause = "ORDER BY"
	order, err := b.bindOrder(s.Order, projection, targetNames, "ORDER BY")
	if err != nil {
		return nil, err
	}
	var on []sortKey
	var onFamilies []queryGrouping
	if s.Distinct != nil && len(s.Distinct.On) != 0 {
		on, onFamilies, err = b.resolveDistinctOn(s, order, projection, targetNames)
		if err != nil {
			return nil, err
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
				return nil, expr.ErrorAt("42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", parser.ExpressionStart(s.Order.Keys[i].Expr))
			}
			distinctOrder[i] = slot
		}
	}
	if err := b.resolveWindows(); err != nil {
		return nil, err
	}
	if (b.params.noParameters || b.consumer.materializedDefinition) && s.Order != nil {
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
	if (b.params.noParameters || b.consumer.materializedDefinition) && len(on) != 0 {
		frozen := *s.Distinct
		frozen.On = append([]parser.Expr(nil), s.Distinct.On...)
		for i, raw := range frozen.On {
			if ordinal := on[i].outputOrdinal; ordinal != 0 {
				frozen.On[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: parser.ExpressionStart(raw)}}
			} else if err := b.captureViewLiterals(raw, on[i].expression, viewDistinctOnLiteral, i); err != nil {
				return nil, err
			}
		}
		s.Distinct = &frozen
	}
	bounds, err := b.bindQueryBounds(s.Bounds, true)
	if err != nil {
		return nil, err
	}
	results := make([]catalog.Column, width)
	regclassOutput := false
	for i, expression := range projection {
		if !contextual || plainDistinct {
			var err error
			expression, err = b.finalizeProjection(expression)
			if err != nil {
				return nil, err
			}
			projection[i] = expression
		}
		if (b.params.noParameters || b.consumer.materializedDefinition) && literalTargets[i] >= 0 && expression.ResultUsage().Type != nil {
			target := literalTargets[i]
			if err := b.captureViewLiterals(s.Targets[target], expression, viewTargetLiteral, target); err != nil {
				return nil, err
			}
		}
		result := catalog.Column{Name: "?column?", Usage: expression.ResultUsage()}
		regclassOutput = regclassOutput || result.Usage.Type.ContainsCatalogIdentifier()
		result.Name = targetNames[i]
		if c, ok := expression.(columnExpression); ok && c.scope == 1 && c.frame == b.frameID() {
			result = namespace.frame[c.slot]
			result.Name = targetNames[i]
		}
		if operation, ok := expression.(groupingExpression); ok && operation.frame == b.frameID() {
			result.Nullable, result.NullabilityKnown = false, true
		}
		for keyIndex, key := range keys {
			if !b.sameGroupExpression(expression, key) {
				continue
			}
			for _, set := range sets {
				present := false
				for _, slot := range set {
					if slot == keyIndex {
						present = true
						break
					}
				}
				if !present {
					result.Nullable, result.NullabilityKnown = true, true
					break
				}
			}
		}
		if aggregate, ok := expression.(aggregateExpression); ok && aggregate.frame == b.frameID() {
			operation := b.grouping.aggregates[aggregate.slot].operation
			switch operation {
			case functions.AggregateJSON, functions.AggregateJSONStrict, functions.AggregateJSONArray, functions.AggregateJSONArrayAbsent, functions.AggregateJSONObject, functions.AggregateJSONObjectStrict:
				// JSON aggregate projections expose computed nullability, including
				// empty/grouped/filter output, rather than a source-column claim.
				result.Nullable, result.NullabilityKnown = false, false
			default:
				result.NullabilityKnown = true
				result.Nullable = operation != functions.AggregateCount && operation != functions.AggregateRegrCount && !operation.Hypothetical()
			}
		}
		if window, ok := expression.(*windowExpression); ok {
			result.Nullable, result.NullabilityKnown = window.nullable, true
		}
		if metadata, ok := expression.(expr.ResultNullability); ok {
			if nullable, known := metadata.ResultNullability(expr.ResultSelect); known {
				result.Nullable, result.NullabilityKnown = nullable, true
			}
		}
		results[i] = result
	}
	var distinctFamilies []queryGrouping
	if plainDistinct {
		if width == 0 {
			return nil, sqlError("42601", "SELECT DISTINCT must have at least one column")
		}
		distinctFamilies = make([]queryGrouping, width)
		for i, expression := range projection {
			distinctFamilies[i], err = b.bindDistinctGrouping(expression)
			if err != nil {
				return nil, err
			}
		}
	}
	grouped := s.GroupBy != nil || b.grouping.active || len(b.grouping.aggregates) > 0 || having != nil
	var stringAnalysis []expr.Expr
	if b.params.noParameters || b.consumer.materializedDefinition {
		stringAnalysis = append(stringAnalysis, projection...)
		stringAnalysis = append(stringAnalysis, keys...)
	}
	if b.query.rowDiagnosticMode != 0 {
		suffix := ""
		for _, expression := range projection {
			if querySetDepth(expression) != 0 {
				suffix = "set-returning functions in the target list"
				break
			}
		}
		switch {
		case s.Distinct != nil:
			suffix = "DISTINCT clause"
		case s.GroupBy != nil:
			suffix = "GROUP BY clause"
		case s.Having != nil:
			suffix = "HAVING clause"
		case len(b.grouping.aggregates) != 0:
			suffix = "aggregate functions"
		case len(b.grouping.windowCalls) != 0:
			suffix = "window functions"
		case b.clause.queryHasTargetSet:
			suffix = "set-returning functions in the target list"
		}
		if suffix != "" {
			mode := queryRowClause(b.query.rowDiagnosticMode)
			if len(s.Locking) != 0 {
				mode = s.Locking[0].Mode
			}
			return nil, sqlError("0A000", mode+" is not allowed with "+suffix)
		}
	}
	if grouped {
		runtimeKeys := append([]expr.Expr(nil), keys...)
		root, err = b.lowerQuerySets(root, runtimeKeys, nil, false)
		if err != nil {
			return nil, err
		}
		for i, call := range b.grouping.aggregates {
			for j, expr := range call.direct {
				direct, err := b.rewriteOrderedSetDirect(expr, keys)
				if err != nil {
					return nil, err
				}
				b.grouping.aggregates[i].direct[j] = direct
			}
		}
		schema := make([]catalog.Column, len(keys)+len(b.grouping.aggregates)+1)
		for i, key := range keys {
			schema[i] = catalog.Column{Usage: key.ResultUsage()}
		}
		for i, call := range b.grouping.aggregates {
			schema[len(keys)+i] = catalog.Column{Usage: call.provider.ResultUsage()}
		}
		schema[len(schema)-1] = catalog.Column{Usage: b.typ(sqltype.Int4).Usage()}
		root = &aggregateNode{querySchema: schema, child: root, keys: runtimeKeys, grouping: comparisons, sets: sets, groupingActive: b.grouping.active, calls: append([]aggregateCall(nil), b.grouping.aggregates...)}
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
		for i := range on {
			on[i].expression, err = b.rewriteGroup(on[i].expression, keys)
			if err != nil {
				return nil, err
			}
		}
	}
	windowCompanions := append(stringSortExpressions(order), stringSortExpressions(on)...)
	root, err = b.finishWindows(root, keys, grouped, projection, order, on, projection, windowCompanions)
	replaceStringSortExpressions(order, windowCompanions[:len(order)])
	replaceStringSortExpressions(on, windowCompanions[len(order):])
	if err != nil {
		return nil, err
	}
	if plainDistinct {
		root, err = b.lowerQuerySets(root, projection, nil, true)
		if err != nil {
			return nil, err
		}
		root = &distinctNode{querySchema: results, child: root, expressions: projection, grouping: distinctFamilies}
		slots := make([]expr.Expr, len(projection))
		for i, expression := range projection {
			slots[i] = columnExpression{Info: expr.InfoOf(expression), scope: 1, frame: b.frameID(), slot: i}
		}
		for i := range order {
			order[i].expression = slots[distinctOrder[i]]
		}
		projection = slots
	}
	earlySets := append(stringSortExpressions(order), stringSortExpressions(on)...)
	earlyWidth := len(earlySets)
	earlySets, earlyTemplates := querySetPeerClosure(earlySets, [][]expr.Expr{projection})
	root, err = b.lowerQuerySets(root, earlySets, [][]expr.Expr{projection}, false, earlyTemplates)
	if err != nil {
		return nil, err
	}
	replaceStringSortExpressions(order, earlySets[:len(order)])
	replaceStringSortExpressions(on, earlySets[len(order):earlyWidth])
	if len(on) != 0 {
		root, err = b.finishDistinctOn(root, order, on, onFamilies, projection)
	} else {
		root, err = b.finishOrder(root, order, projection)
	}
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.finishTies(root)
	}
	root, err = b.lowerQuerySets(root, projection, nil, true)
	if err != nil {
		return nil, err
	}
	if len(on) == 0 {
		root = b.scheduleQueryProjection(root, projection, bounds != nil)
	}
	if len(s.Locking) != 0 {
		admission := b.query.lockAdmission.queries[s]
		var local []queryRowMark
		var err error
		if admission != nil {
			local, err = admission.marks, admission.err
		} else {
			local, err = queryLockTargets(s.From, s.Locking, b.query.lockAdmission.virtualCTE)
		}
		if err != nil {
			return nil, err
		}
		indices := make(map[*parser.TableReference]int)
		if admission != nil {
			indices = admission.indices
		} else {
			_ = parser.VisitQuerySources(&parser.Select{From: s.From}, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		}
		marks := make([]queryRowMark, len(b.query.sources))
		for i, source := range b.query.sources {
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
		results = append([]catalog.Column(nil), results...)
	}
	analysis := stringAnalysis
	for _, call := range b.grouping.windowCalls {
		if !call.analysisOnly {
			continue
		}
		if call.argument != nil {
			analysis = append(analysis, call.argument)
		}
		if call.ordinal != nil {
			analysis = append(analysis, call.ordinal)
		}
		if call.defaultValue != nil {
			analysis = append(analysis, call.defaultValue)
		}
		analysis = append(analysis, call.spec.partition...)
		for _, key := range call.spec.order {
			analysis = append(analysis, key.expression)
		}
		if call.spec.frame.start.offset != nil {
			analysis = append(analysis, call.spec.frame.start.offset)
		}
		if call.spec.frame.end.offset != nil {
			analysis = append(analysis, call.spec.frame.end.offset)
		}
	}
	if (b.params.noParameters || b.consumer.materializedDefinition) && len(on) != 0 {
		// Raw captured occurrences can own independently analyzed subquery
		// frames even when SQL99 resolution shares their physical datum.
		for _, key := range order {
			analysis = append(analysis, key.expression)
		}
		for _, key := range on {
			analysis = append(analysis, key.expression)
		}
	}
	for _, definition := range b.grouping.windowRaw {
		spec := b.grouping.windowDefinitions[definition.Name.Text]
		analysis = append(analysis, spec.partition...)
		for _, key := range spec.order {
			analysis = append(analysis, key.expression)
		}
		if spec.frame.start.offset != nil {
			analysis = append(analysis, spec.frame.start.offset)
		}
		if spec.frame.end.offset != nil {
			analysis = append(analysis, spec.frame.end.offset)
		}
	}
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection, analysisExpressions: analysis, frame: b.frameID(), syntax: s, hasTargetSet: b.clause.queryHasTargetSet}, regclassOutput: regclassOutput, visibilitySnapshot: b.consumer.visibilitySnapshot}, nil
}

func (b *expressionBinder) assignOutput(bound expr.Expr, target catalog.Column) (expr.Expr, error) {
	if raw, ok := bound.(unknownOutput); ok {
		return b.bind(raw.raw, expressionPolicy{context: assignmentContext, expected: target.Usage, mode: sqltype.Assignment})
	}
	if source := bound.ResultUsage().Type; source != nil && source.ID() == 2249 && target.Usage.Type.BaseType().IsComposite() {
		// A projected RECORD output no longer has direct constructor or
		// whole-source conversion authority at the destination boundary.
		return nil, b.rowCoercionError(target.Usage, bound.SourcePosition(), "")
	}
	if p, ok := bound.(parameterExpression); ok && p.Usage.Type == nil {
		var err error
		bound, err = b.coerceParameter(p, target.Usage)
		if err != nil {
			return nil, err
		}
	}
	if bound.ResultUsage() == target.Usage {
		return bound, nil
	}
	input, annotations := domainCoercionInput(bound, target.Usage)
	info, err := storageCoercionInfo(b.bindingCatalog(), b.info(target.Usage, bound.SourcePosition(), input.Properties()), input)
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
	if err != nil {
		d := expr.ErrorAt("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, b.diagnosticTypeName(target.Usage.Type), b.diagnosticTypeName(bound.ResultUsage().Type)), bound.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	if plan.RequiresCatalogOutput() {
		b.prepared.regclassConcat = true
	}
	info.Property = coercionProperties(input, plan)
	bound = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
	return storageDestination(bound, target.Usage, info.Pos)
}

// rewriteOrderedSetDirect rewrites an ordered-set aggregate's direct
// arguments over the group keys.
func (b *expressionBinder) rewriteOrderedSetDirect(expr expr.Expr, keys []expr.Expr) (expr.Expr, error) {
	direct, err := b.rewriteGroup(expr, keys)
	if d, ok := err.(*sqlstate.Error); ok && d.Code == "42803" {
		copy := *d
		copy.Detail = "Direct arguments of an ordered-set aggregate must use only grouped columns."
		return nil, &copy
	}
	return direct, err
}
