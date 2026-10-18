package engine

import (
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// bindInsertInput owns destination mapping, admission and demanded-default
// scheduling for ordinary INSERT and each MERGE insertion arm. Its binder is
// shared with the containing statement, including parameter/source authority.
func (e *Engine) bindInsertInput(b *binding, binder *expressionBinder, auth AuthorizationView, s *parser.Insert) error {
	if s.Source == nil {
		binder.aggregateClause = "INSERT VALUES"
	}
	var firstList expandedExpressionList
	if len(s.Targets) == 0 && s.Source == nil {
		var err error
		firstList, err = binder.expandRawList(s.Rows[0])
		if err != nil {
			return err
		}
		width := len(firstList.raw)
		if width > len(b.relation.columns) {
			return insertError("42601", "INSERT has more expressions than target columns", firstList.raw[len(b.relation.columns)].SourcePosition())
		}
		b.targets = make([]int, width)
		for i := range b.targets {
			b.targets[i] = i
		}
	} else {
		b.targets = make([]int, len(s.Targets))
		if err := bindInsertTargets(b.relation, s.Targets, b.targets); err != nil {
			return err
		}
	}
	for i, target := range s.Targets {
		if len(target.Steps) == 0 {
			continue
		}
		if binder.mergeInput {
			return expressionError("0A000", "MERGE partial insertion targets are unsupported", target.Pos)
		}
		if b.arrayTargets == nil {
			b.arrayTargets = make([]*arrayAssignmentTarget, len(b.targets))
		}
		partial, err := binder.bindAssignmentTarget(b.relation.columns[b.targets[i]], target.Steps, target.Pos)
		if err != nil {
			return err
		}
		b.arrayTargets[i] = partial
	}
	e.bindInsertDefaults(b, auth, s.Override)
	if s.Source != nil {
		if err := e.bindInsertSource(b, binder, auth, s); err != nil {
			return err
		}
		return binder.captureRetainedWriteInput(b.defaults, nil, nil)
	}
	binder.aggregateClause = "INSERT VALUES"
	binder.stringSingleInsert = len(s.Rows) == 1 && !binder.mergeInput
	// Keep aggregate signature/child error precedence independent of the
	// stricter multi-row VALUES placement rule for set-returning functions.
	oldRegexpClause := binder.regexpClause
	defer func() { binder.regexpClause = oldRegexpClause }()
	if len(s.Rows) > 1 {
		binder.regexpClause = "VALUES"
	}
	b.expressions = make([][]BoundExpr, len(s.Rows))
	b.insertRows = s.Rows
	width := -1
	rowsExpanded := false
	for rowIndex, written := range s.Rows {
		list := firstList
		if rowIndex != 0 || len(s.Targets) != 0 {
			var err error
			list, err = binder.expandRawList(written)
			if err != nil {
				return err
			}
		}
		row, expanded := list.raw, list.nodes
		if len(list.expansions) != 0 {
			if !rowsExpanded {
				b.insertRows = append([][]parser.Expr(nil), s.Rows...)
				rowsExpanded = true
			}
			b.insertRows[rowIndex] = row
		}
		if width < 0 {
			width = len(row)
		}
		if len(row) != width {
			pos := parser.Position{Offset: -1}
			if len(row) != 0 {
				pos = row[0].SourcePosition()
			}
			return expressionError("42601", "VALUES lists must all be the same length", pos)
		}
		if len(row) > len(b.targets) {
			return insertError("42601", "INSERT has more expressions than target columns", row[len(b.targets)].SourcePosition())
		}
		if rowIndex == 0 && len(s.Targets) != 0 && len(row) < len(b.targets) {
			return insertError("42601", "INSERT has more target columns than expressions", s.Targets[len(row)].Pos)
		}
		b.expressions[rowIndex] = make([]BoundExpr, len(row))
		stringRow := binder.mergeInput
		for _, v := range row {
			stringRow = stringRow || parser.ContainsStringExpression(v)
		}
		if stringRow {
			// Analyze source occurrences once before destination types constrain
			// shared parameters. Unknown literals retain destination authority.
			for j, v := range row {
				if expanded != nil && expanded[j] != nil {
					b.expressions[rowIndex][j] = expanded[j]
					continue
				}
				if _, requested := v.(parser.DefaultExpr); requested {
					continue
				}
				c := b.relation.columns[b.targets[j]]
				if _, generated := b.relation.generations[b.relation.attributes[b.targets[j]]]; generated {
					return generationWriteError(c.Name, false)
				}
				if identity, ok := b.relation.identities[b.relation.attributes[b.targets[j]]]; ok && identity.mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
					return identityInsertError(c.Name)
				}
				if literal, ok := v.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
					b.expressions[rowIndex][j] = unknownOutput{expressionInfo: expressionInfo{pos: v.SourcePosition()}, raw: v}
					continue
				}
				context := assignmentContext
				if binder.mergeInput {
					context = projectionContext
				}
				plan, err := binder.bind(v, expressionPolicy{context: context, deferAssignment: true})
				if err != nil {
					return err
				}
				b.expressions[rowIndex][j] = plan
			}
		}
		for j, v := range row {
			if _, requested := v.(parser.DefaultExpr); requested {
				if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
					return expressionError("0A000", "cannot set an array element to DEFAULT", s.Targets[j].Pos)
				}
				continue
			}
			c := b.relation.columns[b.targets[j]]
			var partial *arrayAssignmentTarget
			if len(b.arrayTargets) != 0 {
				partial = b.arrayTargets[j]
				if partial != nil {
					c = partial.column(c)
				}
			}
			if _, generated := b.relation.generations[b.relation.attributes[b.targets[j]]]; generated {
				return generationWriteError(c.Name, false)
			}
			if identity, ok := b.relation.identities[b.relation.attributes[b.targets[j]]]; ok && identity.mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
				return identityInsertError(c.Name)
			}
			var plan BoundExpr
			var err error
			if expanded != nil && expanded[j] != nil {
				plan, err = binder.assignOutput(expanded[j], c)
			} else if stringRow {
				plan, err = binder.assignOutput(b.expressions[rowIndex][j], c)
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
					copy := *d
					copy.Position = strconv.Itoa(parser.ExpressionStart(v).Character)
					err = &copy
				}
			} else {
				plan, err = binder.bind(v, expressionPolicy{context: assignmentContext, expected: c.Usage, mode: sqltype.Assignment, column: c.Name})
			}
			if err != nil {
				if partial != nil {
					var source *sqltype.Descriptor
					if plan != nil {
						source = plan.ResultUsage().Type
					}
					return partial.diagnostic(binder, err, source)
				}
				if plan == nil && containsValuesAggregateAnalysis(v) {
					return err
				}
				var source *sqltype.Descriptor
				if plan != nil {
					source = plan.ResultUsage().Type
				}
				return insertValueError(auth.catalog, err, c, v, source)
			}
			b.expressions[rowIndex][j] = plan
		}
	}
	if len(b.defaults) != 0 {
		remaining := 0
		for _, expression := range b.defaults {
			if expression != nil {
				remaining++
			}
		}
		for j, target := range b.targets {
			if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
				if b.defaults[target] != nil {
					b.defaults[target] = nil
					remaining--
				}
				continue
			}
			if len(b.identityDiscard) != 0 && b.identityDiscard[target] {
				continue
			}
			if b.defaults[target] == nil {
				continue
			}
			consumed := false
			for _, row := range b.expressions {
				if j >= len(row) || row[j] == nil {
					consumed = true
					break
				}
			}
			if !consumed {
				b.defaults[target] = nil
				remaining--
			}
		}
		if remaining == 0 {
			b.defaults = nil
		}
	}
	if binder.stringSingleInsert {
		if err := binder.lowerStringInsert(b); err != nil {
			return err
		}
	}
	if len(b.relation.identities) != 0 {
		b.insertProjection = make([]int, len(b.relation.columns))
		for i := range b.insertProjection {
			b.insertProjection[i] = -1
		}
		for i, target := range b.targets {
			b.insertProjection[target] = i
		}
	}
	return binder.captureRetainedWriteInput(b.defaults, nil, nil)
}

// Both INSERT and COPY consume these catalog-owned default and identity plans.
func (e *Engine) bindInsertDefaults(b *binding, auth AuthorizationView, override parser.InsertOverride) {
	hasDomains := false
	for _, column := range b.relation.columns {
		hasDomains = hasDomains || column.Usage.Type.HasDomainChecks()
	}
	if len(b.relation.defaults) == 0 && len(b.relation.identities) == 0 && !hasDomains {
		return
	}
	b.defaults = make([]BoundExpr, len(b.relation.columns))
	for i, attribute := range b.relation.attributes {
		b.defaults[i] = b.relation.defaults[attribute].expression
		if b.defaults[i] == nil && b.relation.columns[i].Usage.Type.HasDomainChecks() {
			b.defaults[i] = domainNullInputExpression(b.relation.columns[i].Usage)
		}
		if identity, ok := b.relation.identities[attribute]; ok {
			b.defaults[i] = sequenceCallExpression{expressionInfo: expressionInfo{usage: b.relation.columns[i].Usage, properties: ExprProperties{Volatility: Volatile}}, name: "nextval", ref: ObjectRef{owner: e.domain, Epoch: auth.catalog.Epoch(), Address: identity.sequence}, identity: true}
		}
	}
	if override == parser.OverrideUser {
		b.identityDiscard = make([]bool, len(b.relation.columns))
		for i, attribute := range b.relation.attributes {
			_, b.identityDiscard[i] = b.relation.identities[attribute]
		}
	}
}
