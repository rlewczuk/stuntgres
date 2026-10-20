package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindOutput(raw parser.Expr, contextual bool) (expr.Expr, error) {
	if contextual {
		if v, ok := raw.(parser.LiteralExpr); ok && (v.Value.Kind == parser.StringValue || v.Value.Kind == parser.NullValue) {
			return plan.UnknownOutput{Info: expr.Info{Pos: raw.SourcePosition()}, Raw: raw}, nil
		}
	}
	return b.bind(raw, expressionPolicy{context: projectionContext})
}

// bindSelect is shared by public SELECT, set operands and INSERT producers.
// Contextual outputs defer unknown fallback until their consuming boundary.
func (b *expressionBinder) bindSelect(s *parser.Select, contextual bool) (*plan.Query, error) {
	return b.bindSelectScoped(s, contextual, false)
}
func (b *expressionBinder) bindSelectScoped(s *parser.Select, contextual, disallowParent bool) (query *plan.Query, failure error) {
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
		project := query.Root.(*plan.ProjectNode)
		project.CTEOwner = !parser.HasModifyingCTE(s)
		if parser.HasModifyingCTE(s) {
			b.cte.statementCTEs = definitions
		}
		for _, definition := range definitions {
			if definition.Query != nil {
				project.AnalysisExpressions = append(project.AnalysisExpressions, plan.SubqueryExpr{Info: b.info(b.typ(sqltype.Bool).Usage(), definition.Position, expr.Properties{}), QueryPlan: definition.Query, CTEDefinition: true})
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
			if index < len(selected) && selected[index].Mode != 0 {
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
		project := query.Root.(*plan.ProjectNode)
		project.Frame, project.Syntax = b.frameID(), s
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
					pseudo := expression.(plan.ColumnExpr)
					projection = append(projection, expression)
					targetNames = append(targetNames, column.Name)
					literalTargets = append(literalTargets, -1)
					b.capture.stars[path] = append(b.capture.stars[path], viewColumn{ruleRef: pseudo.Origin, ruleSide: pseudo.Frame})
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
			projection = append(projection, expansion.Fields...)
			targetNames = append(targetNames, expansion.Names...)
			if b.params.noParameters || b.consumer.materializedDefinition {
				path := viewLiteralPath{query: b.frameID(), clause: viewTargetLiteral, target: target}
				ordinal := 0
				if err := b.captureCompositeExpansion(field, expansion, path, &ordinal); err != nil {
					return nil, err
				}
				for range expansion.Fields {
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
			if source, ok := root.(*plan.SourceNode); ok && len(b.query.sources) == 1 && b.query.sources[0].mark.Mode == 0 {
				b.query.sources[0].partialCandidate = b.query.partialIndexCandidate(source.Ref, filter)
			}
		}
		root = &plan.FilterNode{QuerySchema: root.Schema(), Child: root, Predicate: filter}
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
	var on []plan.SortKey
	var onFamilies []plan.Grouping
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
			slot := key.OutputOrdinal - 1
			if slot < 0 {
				for j, expression := range projection {
					if b.sameGroupExpression(key.Expression, expression) {
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
			if ordinal := order[i].OutputOrdinal; ordinal != 0 {
				// Freeze the exact resolved output occurrence, independently of
				// equivalent expression syntax or a view column-name override.
				frozen.Keys[i].Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: key.Pos}}
				continue
			}
			if err := b.captureViewLiterals(key.Expr, order[i].Expression, viewOrderLiteral, i); err != nil {
				return nil, err
			}
		}
		s.Order = &frozen
	}
	if (b.params.noParameters || b.consumer.materializedDefinition) && len(on) != 0 {
		frozen := *s.Distinct
		frozen.On = append([]parser.Expr(nil), s.Distinct.On...)
		for i, raw := range frozen.On {
			if ordinal := on[i].OutputOrdinal; ordinal != 0 {
				frozen.On[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: parser.ExpressionStart(raw)}}
			} else if err := b.captureViewLiterals(raw, on[i].Expression, viewDistinctOnLiteral, i); err != nil {
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
		if c, ok := expression.(plan.ColumnExpr); ok && c.Scope == 1 && c.Frame == b.frameID() {
			result = namespace.frame[c.Slot]
			result.Name = targetNames[i]
		}
		if operation, ok := expression.(plan.GroupingExpr); ok && operation.Frame == b.frameID() {
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
		if aggregate, ok := expression.(plan.AggregateExpr); ok && aggregate.Frame == b.frameID() {
			operation := b.grouping.aggregates[aggregate.Slot].Operation
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
		if window, ok := expression.(*plan.WindowExpr); ok {
			result.Nullable, result.NullabilityKnown = window.Nullable, true
		}
		if metadata, ok := expression.(expr.ResultNullability); ok {
			if nullable, known := metadata.ResultNullability(expr.ResultSelect); known {
				result.Nullable, result.NullabilityKnown = nullable, true
			}
		}
		results[i] = result
	}
	var distinctFamilies []plan.Grouping
	if plainDistinct {
		if width == 0 {
			return nil, sqlError("42601", "SELECT DISTINCT must have at least one column")
		}
		distinctFamilies = make([]plan.Grouping, width)
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
			for j, expr := range call.Direct {
				direct, err := b.rewriteOrderedSetDirect(expr, keys)
				if err != nil {
					return nil, err
				}
				b.grouping.aggregates[i].Direct[j] = direct
			}
		}
		schema := make([]catalog.Column, len(keys)+len(b.grouping.aggregates)+1)
		for i, key := range keys {
			schema[i] = catalog.Column{Usage: key.ResultUsage()}
		}
		for i, call := range b.grouping.aggregates {
			schema[len(keys)+i] = catalog.Column{Usage: call.Provider.ResultUsage()}
		}
		schema[len(schema)-1] = catalog.Column{Usage: b.typ(sqltype.Int4).Usage()}
		root = &plan.AggregateNode{QuerySchema: schema, Child: root, Keys: runtimeKeys, Grouping: comparisons, Sets: sets, GroupingActive: b.grouping.active, Calls: append([]plan.AggregateCall(nil), b.grouping.aggregates...)}
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
			root = &plan.FilterNode{QuerySchema: root.Schema(), Child: root, Predicate: having}
		}
		for i := range order {
			order[i].Expression, err = b.rewriteGroup(order[i].Expression, keys)
			if err != nil {
				return nil, err
			}
		}
		for i := range on {
			on[i].Expression, err = b.rewriteGroup(on[i].Expression, keys)
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
		root = &plan.DistinctNode{QuerySchema: results, Child: root, Expressions: projection, Grouping: distinctFamilies}
		slots := make([]expr.Expr, len(projection))
		for i, expression := range projection {
			slots[i] = plan.ColumnExpr{Info: expr.InfoOf(expression), Scope: 1, Frame: b.frameID(), Slot: i}
		}
		for i := range order {
			order[i].Expression = slots[distinctOrder[i]]
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
		bounds.FinishTies(root)
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
		var local []plan.RowMark
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
		marks := make([]plan.RowMark, len(b.query.sources))
		for i, source := range b.query.sources {
			if index, ok := indices[source.table]; ok && index < len(local) {
				marks[i] = local[index]
			}
		}
		root = &plan.RowLockNode{QuerySchema: root.Schema(), Child: root, Marks: marks, FirstMode: queryRowMode(s.Locking[0].Mode)}
	}
	if bounds != nil {
		bounds.QuerySchema, bounds.Child = root.Schema(), root
		root = bounds
	}
	if contextual && plainDistinct {
		// INSERT assignment mutates only the final output descriptors, never the
		// source-typed descriptors retained by DISTINCT and its ordering/bounds.
		results = append([]catalog.Column(nil), results...)
	}
	analysis := stringAnalysis
	for _, call := range b.grouping.windowCalls {
		if !call.AnalysisOnly {
			continue
		}
		if call.Argument != nil {
			analysis = append(analysis, call.Argument)
		}
		if call.Ordinal != nil {
			analysis = append(analysis, call.Ordinal)
		}
		if call.DefaultValue != nil {
			analysis = append(analysis, call.DefaultValue)
		}
		analysis = append(analysis, call.Spec.Partition...)
		for _, key := range call.Spec.Order {
			analysis = append(analysis, key.Expression)
		}
		if call.Spec.Frame.Start.Offset != nil {
			analysis = append(analysis, call.Spec.Frame.Start.Offset)
		}
		if call.Spec.Frame.End.Offset != nil {
			analysis = append(analysis, call.Spec.Frame.End.Offset)
		}
	}
	if (b.params.noParameters || b.consumer.materializedDefinition) && len(on) != 0 {
		// Raw captured occurrences can own independently analyzed subquery
		// frames even when SQL99 resolution shares their physical datum.
		for _, key := range order {
			analysis = append(analysis, key.Expression)
		}
		for _, key := range on {
			analysis = append(analysis, key.Expression)
		}
	}
	for _, definition := range b.grouping.windowRaw {
		spec := b.grouping.windowDefinitions[definition.Name.Text]
		analysis = append(analysis, spec.Partition...)
		for _, key := range spec.Order {
			analysis = append(analysis, key.Expression)
		}
		if spec.Frame.Start.Offset != nil {
			analysis = append(analysis, spec.Frame.Start.Offset)
		}
		if spec.Frame.End.Offset != nil {
			analysis = append(analysis, spec.Frame.End.Offset)
		}
	}
	return &plan.Query{Root: &plan.ProjectNode{QuerySchema: results, Child: root, Expressions: projection, AnalysisExpressions: analysis, Frame: b.frameID(), Syntax: s, HasTargetSet: b.clause.queryHasTargetSet}, RegclassOutput: regclassOutput, VisibilitySnapshot: b.consumer.visibilitySnapshot}, nil
}

func (b *expressionBinder) assignOutput(bound expr.Expr, target catalog.Column) (expr.Expr, error) {
	if raw, ok := bound.(plan.UnknownOutput); ok {
		return b.bind(raw.Raw, expressionPolicy{context: assignmentContext, expected: target.Usage, mode: sqltype.Assignment})
	}
	if source := bound.ResultUsage().Type; source != nil && source.ID() == 2249 && target.Usage.Type.BaseType().IsComposite() {
		// A projected RECORD output no longer has direct constructor or
		// whole-source conversion authority at the destination boundary.
		return nil, b.rowCoercionError(target.Usage, bound.SourcePosition(), "")
	}
	if p, ok := bound.(plan.ParameterExpr); ok && p.Usage.Type == nil {
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
	coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
	if err != nil {
		d := expr.ErrorAt("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, b.diagnosticTypeName(target.Usage.Type), b.diagnosticTypeName(bound.ResultUsage().Type)), bound.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	if coercionPlan.RequiresCatalogOutput() {
		b.prepared.regclassConcat = true
	}
	info.Property = coercionProperties(input, coercionPlan)
	bound = restoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
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
