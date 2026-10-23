package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindOutput(raw parser.Expr, contextual bool) (expr.Expr, error) {
	if contextual {
		if v, ok := raw.(parser.LiteralExpr); ok && (v.Value.Kind == parser.StringValue || v.Value.Kind == parser.NullValue) {
			return plan.UnknownOutput{Info: expr.Info{Pos: raw.SourcePosition()}, Raw: raw}, nil
		}
	}
	return b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
}

// BindSelect is shared by public SELECT, set operands and INSERT producers.
// Contextual outputs defer unknown fallback until their consuming boundary.
func (b *Binder) BindSelect(s *parser.Select, contextual bool) (*plan.Query, error) {
	return b.bindSelectScoped(s, contextual, false)
}
func (b *Binder) bindSelectScoped(s *parser.Select, contextual, disallowParent bool) (query *plan.Query, failure error) {
	entry, scanStart := b.cte.analysisDepth == 0, b.cte.scanOrdinal
	defer func() {
		if entry && failure == nil && query != nil && b.cte.scanOrdinal != scanStart {
			b.cte.executables = append(b.cte.executables, query)
		}
	}()
	if s.Into != nil {
		return nil, insertError("42601", "SELECT ... INTO is not allowed here", s.Into.Parts[0].Pos)
	}
	if b.Query.lockAdmission == nil || b.Query.lockAdmission.Queries == nil {
		admission, _, _, err := NewLockAdmission(s)
		if err != nil {
			return nil, err
		}
		b.Query.lockAdmission = &admission
	}
	restore := b.EnterQuery(disallowParent)
	defer restore()
	defer func() {
		if failure == nil && query != nil && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
			b.NormalizeStoredQuerySyntax(s)
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
				project.AnalysisExpressions = append(project.AnalysisExpressions, plan.SubqueryExpr{Info: b.Info(b.Type(sqltype.Bool).Usage(), definition.Position, expr.Properties{}), QueryPlan: definition.Query, CTEEdge: true})
			}
		}
	}()
	plainDistinct := s.Distinct != nil && len(s.Distinct.On) == 0
	if s.Set != nil {
		if mode := FirstSetRowClause(s); mode != "" {
			return nil, sqlError("0A000", mode+" is not allowed with UNION/INTERSECT/EXCEPT")
		}
		if mode := b.inheritedDerivedSetMode(s); mode != "" {
			return nil, sqlError("0A000", mode+" is not allowed with UNION/INTERSECT/EXCEPT")
		}
		return b.bindSet(s)
	}
	if len(s.Locking) != 0 {
		b.Query.rowDiagnosticMode = RowLockMode(s.Locking[0].Mode)
	}
	if disallowParent {
		selected, _ := RowLockTargets(s.From, []parser.RowLockClause{{Mode: "FOR KEY SHARE"}}, b.Query.lockAdmission.VirtualCTE)
		index := 0
		_ = parser.VisitQuerySources(&parser.Select{From: s.From}, nil, func(table *parser.TableReference, _ *parser.Select) error {
			if index < len(selected) && selected[index].Mode != 0 {
				b.Query.rowDiagnosticMode = exec.StrongerQueryRowMode(b.Query.rowDiagnosticMode, RowLockDiagnostic(b.Query.lockAdmission.TableMark(table)))
			}
			index++
			return nil
		})
	}
	if s.Values != nil {
		query, err := b.BindValues(s.Values, false)
		if err != nil {
			return nil, err
		}
		// VALUES cell analysis precedes row-lock legality; retain the wrapper
		// through INSERT and derived conversion so this owner sees every clause.
		if len(s.Locking) != 0 {
			return nil, sqlError("0A000", s.Locking[0].Mode+" cannot be applied to VALUES")
		}
		project := query.Root.(*plan.ProjectNode)
		project.Frame, project.Syntax = b.FrameID(), s
		return query, err
	}
	root, namespace, err := b.BindFrom(s.From)
	if err != nil {
		return nil, err
	}
	b.Query.Namespace = &namespace
	b.Clause.AggregateClause, b.Grouping.Aggregates = "SELECT", nil
	b.Grouping.windowRaw = s.Windows
	var projection []expr.Expr
	var targetNames []string
	var literalTargets []int
	for target, raw := range s.Targets {
		if err := b.Ctx.Err(); err != nil {
			return nil, err
		}
		if star, ok := raw.(parser.StarExpr); ok {
			if b.Target.RuleNamespace != nil && len(star.Qualifier.Parts) == 1 && (star.Qualifier.Parts[0].Text == "old" || star.Qualifier.Parts[0].Text == "new") {
				path := ViewLiteralPath{Query: b.FrameID(), Clause: ViewTargetLiteral, Target: target}
				if b.Capture.Stars == nil {
					b.Capture.Stars = make(map[ViewLiteralPath][]ViewColumn)
				}
				b.Capture.Stars[path] = nil
				for _, column := range b.Target.RuleNamespace.Relation.Columns {
					name := star.Qualifier
					name.Parts = []parser.Identifier{star.Qualifier.Parts[0], {Text: column.Name}}
					expression, _, err := b.RuleColumn(parser.ColumnExpr{Name: name, Pos: star.Pos})
					if err != nil {
						return nil, err
					}
					pseudo := expression.(plan.ColumnExpr)
					projection = append(projection, expression)
					targetNames = append(targetNames, column.Name)
					literalTargets = append(literalTargets, -1)
					b.Capture.Stars[path] = append(b.Capture.Stars[path], ViewColumn{RuleRef: pseudo.Origin, RuleSide: pseudo.Frame})
				}
				continue
			}
			columns := namespace.Columns
			frame := b.FrameID()
			starPath := ViewLiteralPath{Query: frame, Clause: ViewTargetLiteral, Target: target}
			var returningScope *queryBindingScope
			newValues := false
			var recordQualifier *QueryQualifier
			if len(star.Qualifier.Parts) != 0 {
				returningScope, newValues = b.outerReturningScope(star.Qualifier)
				if returningScope != nil {
					columns = returningScope.namespace.Columns[:len(returningScope.relation.Columns)]
					frame = returningScope.frame
				} else {
					q, err := b.queryQualifier(star.Qualifier, star.Pos)
					if err != nil {
						return nil, err
					}
					columns, frame = q.Columns, b.Query.resolvedQueryFrame
					recordQualifier = q
				}
			} else if s.From == nil {
				return nil, expr.ErrorAt("42601", "SELECT * with no tables specified is not valid", star.Pos)
			}
			if returningScope == nil {
				frozenColumns, err := b.JSONBRecordStarColumns(columns, frame, star, recordQualifier)
				if err != nil {
					return nil, err
				}
				columns = frozenColumns
			}
			if b.Params.NoParameters || b.Consumer.frozenDefinition {
				if b.Capture.Stars == nil {
					b.Capture.Stars = make(map[ViewLiteralPath][]ViewColumn)
				}
				b.Capture.Stars[starPath] = nil
			}
			for _, c := range columns {
				if c.HideStar {
					continue
				}
				var expr expr.Expr
				if returningScope != nil {
					expr = b.outerReturningColumnAt(returningScope, c.Physical, star.Pos, newValues)
				} else {
					expr = b.queryColumnInFrame(c, star.Pos, frame)
				}
				projection = append(projection, expr)
				targetNames = append(targetNames, c.Descriptor.Name)
				if b.Params.NoParameters || b.Consumer.frozenDefinition {
					b.Capture.Stars[starPath] = append(b.Capture.Stars[starPath], ViewColumn{Frame: frame, Slot: c.Slot})
					literalTargets = append(literalTargets, -1)
				}
			}
		} else if field, ok := raw.(parser.FieldExpr); ok && field.Expansion {
			expansion, err := b.ExpandComposite(field)
			if err != nil {
				return nil, err
			}
			projection = append(projection, expansion.Fields...)
			targetNames = append(targetNames, expansion.Names...)
			if b.Params.NoParameters || b.Consumer.frozenDefinition {
				path := ViewLiteralPath{Query: b.FrameID(), Clause: ViewTargetLiteral, Target: target}
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
			if b.Params.NoParameters || b.Consumer.frozenDefinition {
				literalTargets = append(literalTargets, target)
			}
			name, _ := ExpressionName(raw, expr.ResultUsage(), expr)
			if target < len(s.Labels) && s.Labels[target].Text != "" {
				name = s.Labels[target].Text
			}
			targetNames = append(targetNames, name)
		}
	}
	width := len(projection)
	b.Clause.AggregateClause = "WHERE"
	if s.Where != nil {
		filter, err := b.BindBoolean(s.Where, FilterContext, "WHERE")
		if err != nil {
			return nil, err
		}
		if b.Params.NoParameters || b.Consumer.frozenDefinition {
			if err := b.CaptureViewLiterals(s.Where, filter, ViewWhereLiteral, 0); err != nil {
				return nil, err
			}
		}
		if !contextual && !b.Params.NoParameters && !b.Consumer.frozenDefinition && len(s.Locking) == 0 {
			if source, ok := root.(*plan.SourceNode); ok && len(b.Query.Sources) == 1 && b.Query.Sources[0].Mark.Mode == 0 {
				b.Query.Sources[0].PartialCandidate = b.Query.PartialIndexCandidate(source.Ref, filter)
			}
		}
		root = &plan.FilterNode{QuerySchema: root.Schema(), Child: root, Predicate: filter}
	}
	b.Clause.AggregateClause = "GROUP BY"
	keys, comparisons, sets, err := b.BindGroupClause(s.GroupBy, projection, targetNames)
	if err != nil {
		return nil, err
	}
	b.Grouping.GroupSets = sets
	b.Clause.AggregateClause = "HAVING"
	var having expr.Expr
	if s.Having != nil {
		var err error
		having, err = b.BindBoolean(s.Having, ProjectionContext, "HAVING")
		if err != nil {
			return nil, err
		}
		if b.Params.NoParameters || b.Consumer.frozenDefinition {
			if err := b.CaptureViewLiterals(s.Having, having, ViewHavingLiteral, 0); err != nil {
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
	b.Clause.AggregateClause = "ORDER BY"
	order, err := b.BindOrder(s.Order, projection, targetNames, "ORDER BY")
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
					if b.SameGroupExpression(key.Expression, expression) {
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
	if (b.Params.NoParameters || b.Consumer.frozenDefinition) && s.Order != nil {
		frozen := *s.Order
		frozen.Keys = append([]parser.OrderKey(nil), s.Order.Keys...)
		for i, key := range frozen.Keys {
			if ordinal := order[i].OutputOrdinal; ordinal != 0 {
				// Freeze the exact resolved output occurrence, independently of
				// equivalent expression syntax or a view column-name override.
				frozen.Keys[i].Expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: key.Pos}}
				continue
			}
			if err := b.CaptureViewLiterals(key.Expr, order[i].Expression, ViewOrderLiteral, i); err != nil {
				return nil, err
			}
		}
		s.Order = &frozen
	}
	if (b.Params.NoParameters || b.Consumer.frozenDefinition) && len(on) != 0 {
		frozen := *s.Distinct
		frozen.On = append([]parser.Expr(nil), s.Distinct.On...)
		for i, raw := range frozen.On {
			if ordinal := on[i].OutputOrdinal; ordinal != 0 {
				frozen.On[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(ordinal), Pos: parser.ExpressionStart(raw)}}
			} else if err := b.CaptureViewLiterals(raw, on[i].Expression, ViewDistinctOnLiteral, i); err != nil {
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
			expression, err = b.FinalizeProjection(expression)
			if err != nil {
				return nil, err
			}
			projection[i] = expression
		}
		if (b.Params.NoParameters || b.Consumer.frozenDefinition) && literalTargets[i] >= 0 && expression.ResultUsage().Type != nil {
			target := literalTargets[i]
			if err := b.CaptureViewLiterals(s.Targets[target], expression, ViewTargetLiteral, target); err != nil {
				return nil, err
			}
		}
		result := catalog.Column{Name: "?column?", Usage: expression.ResultUsage()}
		regclassOutput = regclassOutput || result.Usage.Type.ContainsCatalogIdentifier()
		result.Name = targetNames[i]
		if c, ok := expression.(plan.ColumnExpr); ok && c.Scope == 1 && c.Frame == b.FrameID() {
			result = namespace.Frame[c.Slot]
			result.Name = targetNames[i]
		}
		if operation, ok := expression.(plan.GroupingExpr); ok && operation.Frame == b.FrameID() {
			result.Nullable, result.NullabilityKnown = false, true
		}
		for keyIndex, key := range keys {
			if !b.SameGroupExpression(expression, key) {
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
		if aggregate, ok := expression.(plan.AggregateExpr); ok && aggregate.Frame == b.FrameID() {
			operation := b.Grouping.Aggregates[aggregate.Slot].Operation
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
	grouped := s.GroupBy != nil || b.Grouping.active || len(b.Grouping.Aggregates) > 0 || having != nil
	var stringAnalysis []expr.Expr
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		stringAnalysis = append(stringAnalysis, projection...)
		stringAnalysis = append(stringAnalysis, keys...)
	}
	if b.Query.rowDiagnosticMode != 0 {
		suffix := ""
		for _, expression := range projection {
			if exec.QuerySetDepth(expression) != 0 {
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
		case len(b.Grouping.Aggregates) != 0:
			suffix = "aggregate functions"
		case len(b.Grouping.WindowCalls) != 0:
			suffix = "window functions"
		case b.Clause.QueryHasTargetSet:
			suffix = "set-returning functions in the target list"
		}
		if suffix != "" {
			mode := RowLockClause(b.Query.rowDiagnosticMode)
			if len(s.Locking) != 0 {
				mode = s.Locking[0].Mode
			}
			return nil, sqlError("0A000", mode+" is not allowed with "+suffix)
		}
	}
	if grouped {
		runtimeKeys := append([]expr.Expr(nil), keys...)
		root, err = b.LowerQuerySets(root, runtimeKeys, nil, false)
		if err != nil {
			return nil, err
		}
		for i, call := range b.Grouping.Aggregates {
			for j, expr := range call.Direct {
				direct, err := b.rewriteOrderedSetDirect(expr, keys)
				if err != nil {
					return nil, err
				}
				b.Grouping.Aggregates[i].Direct[j] = direct
			}
		}
		schema := make([]catalog.Column, len(keys)+len(b.Grouping.Aggregates)+1)
		for i, key := range keys {
			schema[i] = catalog.Column{Usage: key.ResultUsage()}
		}
		for i, call := range b.Grouping.Aggregates {
			schema[len(keys)+i] = catalog.Column{Usage: call.Provider.ResultUsage()}
		}
		schema[len(schema)-1] = catalog.Column{Usage: b.Type(sqltype.Int4).Usage()}
		root = &plan.AggregateNode{QuerySchema: schema, Child: root, Keys: runtimeKeys, Grouping: comparisons, Sets: sets, GroupingActive: b.Grouping.active, Calls: append([]plan.AggregateCall(nil), b.Grouping.Aggregates...)}
		for i, expr := range projection {
			var err error
			projection[i], err = b.RewriteGroup(expr, keys)
			if err != nil {
				return nil, err
			}
		}
		if having != nil {
			var err error
			having, err = b.RewriteGroup(having, keys)
			if err != nil {
				return nil, err
			}
			root = &plan.FilterNode{QuerySchema: root.Schema(), Child: root, Predicate: having}
		}
		for i := range order {
			order[i].Expression, err = b.RewriteGroup(order[i].Expression, keys)
			if err != nil {
				return nil, err
			}
		}
		for i := range on {
			on[i].Expression, err = b.RewriteGroup(on[i].Expression, keys)
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
		root, err = b.LowerQuerySets(root, projection, nil, true)
		if err != nil {
			return nil, err
		}
		root = &plan.DistinctNode{QuerySchema: results, Child: root, Expressions: projection, Grouping: distinctFamilies}
		slots := make([]expr.Expr, len(projection))
		for i, expression := range projection {
			slots[i] = plan.ColumnExpr{Info: expr.InfoOf(expression), Scope: 1, Frame: b.FrameID(), Slot: i}
		}
		for i := range order {
			order[i].Expression = slots[distinctOrder[i]]
		}
		projection = slots
	}
	earlySets := append(stringSortExpressions(order), stringSortExpressions(on)...)
	earlyWidth := len(earlySets)
	earlySets, earlyTemplates := QuerySetPeerClosure(earlySets, [][]expr.Expr{projection})
	root, err = b.LowerQuerySets(root, earlySets, [][]expr.Expr{projection}, false, earlyTemplates)
	if err != nil {
		return nil, err
	}
	replaceStringSortExpressions(order, earlySets[:len(order)])
	replaceStringSortExpressions(on, earlySets[len(order):earlyWidth])
	if len(on) != 0 {
		root, err = b.FinishDistinctOn(root, order, on, onFamilies, projection)
	} else {
		root, err = b.FinishOrder(root, order, projection)
	}
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.FinishTies(root)
	}
	root, err = b.LowerQuerySets(root, projection, nil, true)
	if err != nil {
		return nil, err
	}
	if len(on) == 0 {
		root = b.ScheduleQueryProjection(root, projection, bounds != nil)
	}
	if len(s.Locking) != 0 {
		admission := b.Query.lockAdmission.Queries[s]
		var local []plan.RowMark
		var err error
		if admission != nil {
			local, err = admission.Marks, admission.Err
		} else {
			local, err = RowLockTargets(s.From, s.Locking, b.Query.lockAdmission.VirtualCTE)
		}
		if err != nil {
			return nil, err
		}
		indices := make(map[*parser.TableReference]int)
		if admission != nil {
			indices = admission.Indices
		} else {
			_ = parser.VisitQuerySources(&parser.Select{From: s.From}, nil, func(table *parser.TableReference, _ *parser.Select) error { indices[table] = len(indices); return nil })
		}
		marks := make([]plan.RowMark, len(b.Query.Sources))
		for i, source := range b.Query.Sources {
			if index, ok := indices[source.Table]; ok && index < len(local) {
				marks[i] = local[index]
			}
		}
		root = &plan.RowLockNode{QuerySchema: root.Schema(), Child: root, Marks: marks, FirstMode: RowLockMode(s.Locking[0].Mode)}
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
	for _, call := range b.Grouping.WindowCalls {
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
	if (b.Params.NoParameters || b.Consumer.frozenDefinition) && len(on) != 0 {
		// Raw captured occurrences can own independently analyzed subquery
		// frames even when SQL99 resolution shares their physical datum.
		for _, key := range order {
			analysis = append(analysis, key.Expression)
		}
		for _, key := range on {
			analysis = append(analysis, key.Expression)
		}
	}
	for _, definition := range b.Grouping.windowRaw {
		spec := b.Grouping.windowDefinitions[definition.Name.Text]
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
	return &plan.Query{Root: &plan.ProjectNode{QuerySchema: results, Child: root, Expressions: projection, AnalysisExpressions: analysis, Frame: b.FrameID(), Syntax: s, HasTargetSet: b.Clause.QueryHasTargetSet}, RegclassOutput: regclassOutput, VisibilitySnapshot: b.Consumer.VisibilitySnapshot}, nil
}

func (b *Binder) assignOutput(bound expr.Expr, target catalog.Column) (expr.Expr, error) {
	if raw, ok := bound.(plan.UnknownOutput); ok {
		return b.Bind(raw.Raw, ExpressionPolicy{Context: AssignmentContext, Expected: target.Usage, Mode: sqltype.Assignment})
	}
	if source := bound.ResultUsage().Type; source != nil && source.ID() == 2249 && target.Usage.Type.BaseType().IsComposite() {
		// A projected RECORD output no longer has direct constructor or
		// whole-source conversion authority at the destination boundary.
		return nil, b.rowCoercionError(target.Usage, bound.SourcePosition(), "")
	}
	if p, ok := bound.(plan.ParameterExpr); ok && p.Usage.Type == nil {
		var err error
		bound, err = b.CoerceParameter(p, target.Usage)
		if err != nil {
			return nil, err
		}
	}
	if bound.ResultUsage() == target.Usage {
		return bound, nil
	}
	input, annotations := DomainCoercionInput(bound, target.Usage)
	info, err := StorageCoercionInfo(b.BindingCatalog(), b.Info(target.Usage, bound.SourcePosition(), input.Properties()), input)
	if err != nil {
		return nil, err
	}
	if selected, handled, selectionErr := b.declaredCoercion(input, info.Usage, sqltype.Assignment, input.SourcePosition(), nil); handled {
		if selectionErr != nil {
			d := expr.ErrorAt("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, b.DiagnosticTypeName(target.Usage.Type), b.DiagnosticTypeName(bound.ResultUsage().Type)), bound.SourcePosition()).(*sqlstate.Error)
			d.Hint = "You will need to rewrite or cast the expression."
			return nil, d
		}
		return RestoreDomainCastCollations(annotations, selected), nil
	}
	coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
	if err != nil {
		d := expr.ErrorAt("42804", fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, target.Name, b.DiagnosticTypeName(target.Usage.Type), b.DiagnosticTypeName(bound.ResultUsage().Type)), bound.SourcePosition())
		d.(*sqlstate.Error).Hint = "You will need to rewrite or cast the expression."
		return nil, d
	}
	if coercionPlan.RequiresCatalogOutput() {
		b.Prepared.RegclassConcat = true
	}
	info.Property = CoercionProperties(input, coercionPlan)
	bound = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	return StorageDestination(bound, target.Usage, info.Pos)
}

// rewriteOrderedSetDirect rewrites an ordered-set aggregate's direct
// arguments over the group keys.
func (b *Binder) rewriteOrderedSetDirect(expr expr.Expr, keys []expr.Expr) (expr.Expr, error) {
	direct, err := b.RewriteGroup(expr, keys)
	if d, ok := err.(*sqlstate.Error); ok && d.Code == "42803" {
		copy := *d
		copy.Detail = "Direct arguments of an ordered-set aggregate must use only grouped columns."
		return nil, &copy
	}
	return direct, err
}
