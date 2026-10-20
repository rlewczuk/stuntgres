package engine

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// bindInsertInput owns destination mapping, admission and demanded-default
// scheduling for ordinary INSERT and each MERGE insertion arm. Its binder is
// shared with the containing statement, including parameter/source authority.
func bindInsertInput(b *binding, binder *expressionBinder, auth AuthorizationView, s *parser.Insert) error {
	if s.Source == nil {
		binder.clause.aggregateClause = "INSERT VALUES"
	}
	var firstList expandedExpressionList
	if len(s.Targets) == 0 && s.Source == nil {
		var err error
		firstList, err = binder.expandRawList(s.Rows[0])
		if err != nil {
			return err
		}
		width := len(firstList.raw)
		if width > len(b.Relation.Columns) {
			return insertError("42601", "INSERT has more expressions than target columns", firstList.raw[len(b.Relation.Columns)].SourcePosition())
		}
		b.Targets = make([]int, width)
		for i := range b.Targets {
			b.Targets[i] = i
		}
	} else {
		b.Targets = make([]int, len(s.Targets))
		if err := bindInsertTargets(b.Relation, s.Targets, b.Targets); err != nil {
			return err
		}
	}
	for i, target := range s.Targets {
		if len(target.Steps) == 0 {
			continue
		}
		if binder.target.mergeInput {
			return expr.ErrorAt("0A000", "MERGE partial insertion targets are unsupported", target.Pos)
		}
		if b.ArrayTargets == nil {
			b.ArrayTargets = make([]*plan.ArrayAssignmentTarget, len(b.Targets))
		}
		partial, err := binder.bindAssignmentTarget(b.Relation.Columns[b.Targets[i]], target.Steps, target.Pos)
		if err != nil {
			return err
		}
		b.ArrayTargets[i] = partial
	}
	domainScheduling := bindInsertDefaults(binder.host.CatalogOwner(), b, auth, s.Override)
	if s.Source != nil {
		if err := bindInsertSource(b, binder, auth, s); err != nil {
			return err
		}
		return binder.captureRetainedWriteInput(b.Insert.Defaults, nil, nil)
	}
	binder.clause.aggregateClause = "INSERT VALUES"
	binder.clause.stringSingleInsert = len(s.Rows) == 1 && !binder.target.mergeInput
	// Keep aggregate signature/child error precedence independent of the
	// stricter multi-row VALUES placement rule for set-returning functions.
	oldRegexpClause := binder.clause.regexpClause
	defer func() { binder.clause.regexpClause = oldRegexpClause }()
	if len(s.Rows) > 1 {
		binder.clause.regexpClause = "VALUES"
	}
	b.Expressions = make([][]expr.Expr, len(s.Rows))
	b.Insert.Rows = s.Rows
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
				b.Insert.Rows = append([][]parser.Expr(nil), s.Rows...)
				rowsExpanded = true
			}
			b.Insert.Rows[rowIndex] = row
		}
		if width < 0 {
			width = len(row)
		}
		if len(row) != width {
			pos := parser.Position{Offset: -1}
			if len(row) != 0 {
				pos = row[0].SourcePosition()
			}
			return expr.ErrorAt("42601", "VALUES lists must all be the same length", pos)
		}
		if len(row) > len(b.Targets) {
			return insertError("42601", "INSERT has more expressions than target columns", row[len(b.Targets)].SourcePosition())
		}
		if rowIndex == 0 && len(s.Targets) != 0 && len(row) < len(b.Targets) {
			return insertError("42601", "INSERT has more target columns than expressions", s.Targets[len(row)].Pos)
		}
		b.Expressions[rowIndex] = make([]expr.Expr, len(row))
		stringRow := binder.target.mergeInput
		for _, v := range row {
			stringRow = stringRow || parser.ContainsStringExpression(v)
		}
		if stringRow {
			// Analyze source occurrences once before destination types constrain
			// shared parameters. Unknown literals retain destination authority.
			for j, v := range row {
				if captured, handled, err := binder.host.ReplayDomainCell(b, s, rowIndex, j); handled || err != nil {
					if err != nil {
						return err
					}
					b.Expressions[rowIndex][j] = captured
					continue
				}
				if expanded != nil && expanded[j] != nil {
					b.Expressions[rowIndex][j] = expanded[j]
					continue
				}
				if _, requested := v.(parser.DefaultExpr); requested {
					continue
				}
				c := b.Relation.Columns[b.Targets[j]]
				if _, generated := b.Relation.ColumnMeta.Generations[b.Relation.Attributes[b.Targets[j]]]; generated {
					return generationWriteError(c.Name, false)
				}
				if identity, ok := b.Relation.ColumnMeta.Identities[b.Relation.Attributes[b.Targets[j]]]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
					return sequence.IdentityInsertError(c.Name)
				}
				if literal, ok := v.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
					b.Expressions[rowIndex][j] = plan.UnknownOutput{Info: expr.Info{Pos: v.SourcePosition()}, Raw: v}
					continue
				}
				context := assignmentContext
				if binder.target.mergeInput {
					context = projectionContext
				}
				plan, err := binder.bind(v, expressionPolicy{context: context, deferAssignment: true})
				if err != nil {
					return err
				}
				b.Expressions[rowIndex][j] = plan
			}
		}
		for j, v := range row {
			if _, requested := v.(parser.DefaultExpr); requested {
				if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
					return expr.ErrorAt("0A000", "cannot set an array element to DEFAULT", s.Targets[j].Pos)
				}
				continue
			}
			c := b.Relation.Columns[b.Targets[j]]
			var partial *plan.ArrayAssignmentTarget
			if len(b.ArrayTargets) != 0 {
				partial = b.ArrayTargets[j]
				if partial != nil {
					c = partial.Column(c)
				}
			}
			if _, generated := b.Relation.ColumnMeta.Generations[b.Relation.Attributes[b.Targets[j]]]; generated {
				return generationWriteError(c.Name, false)
			}
			if identity, ok := b.Relation.ColumnMeta.Identities[b.Relation.Attributes[b.Targets[j]]]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
				return sequence.IdentityInsertError(c.Name)
			}
			var plan expr.Expr
			var err error
			if captured, handled, captureErr := binder.host.ReplayDomainCell(b, s, rowIndex, j); handled || captureErr != nil {
				plan, err = captured, captureErr
			} else if expanded != nil && expanded[j] != nil {
				plan, err = binder.assignOutput(expanded[j], c)
			} else if stringRow {
				plan, err = binder.assignOutput(b.Expressions[rowIndex][j], c)
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
					return binder.arrayAssignmentDiagnostic(partial, err, source)
				}
				if plan == nil && containsValuesAggregateAnalysis(v) {
					return err
				}
				var source *sqltype.Descriptor
				if plan != nil {
					source = plan.ResultUsage().Type
				}
				return insertValueError(auth.Catalog, err, c, v, source)
			}
			b.Expressions[rowIndex][j] = plan
		}
	}
	if len(b.Insert.Defaults) != 0 {
		remaining := 0
		for _, expression := range b.Insert.Defaults {
			if expression != nil {
				remaining++
			}
		}
		for j, target := range b.Targets {
			if len(b.ArrayTargets) != 0 && b.ArrayTargets[j] != nil {
				if b.Insert.Defaults[target] != nil {
					b.Insert.Defaults[target] = nil
					remaining--
				}
				continue
			}
			if len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[target] {
				continue
			}
			if b.Insert.Defaults[target] == nil {
				continue
			}
			consumed := false
			for _, row := range b.Expressions {
				if j >= len(row) || row[j] == nil {
					consumed = true
					break
				}
			}
			if !consumed {
				b.Insert.Defaults[target] = nil
				remaining--
			}
		}
		if remaining == 0 {
			b.Insert.Defaults = nil
		}
	}
	if binder.clause.stringSingleInsert {
		if err := binder.lowerStringInsert(b); err != nil {
			return err
		}
	}
	if len(b.Relation.ColumnMeta.Identities) != 0 || domainScheduling && len(b.Insert.Defaults) != 0 {
		b.Insert.Projection = make([]int, len(b.Relation.Columns))
		for i := range b.Insert.Projection {
			b.Insert.Projection[i] = -1
		}
		for i, target := range b.Targets {
			b.Insert.Projection[target] = i
		}
	}
	return binder.captureRetainedWriteInput(b.Insert.Defaults, nil, nil)
}

// INSERT, COPY and foreign actions share catalog-owned default/identity plans.
// The result tells VALUES binding whether domain inputs/defaults require the
// existing destination-attribute projection rather than default-first demand.
func bindInsertDefaults(owner *catalog.Owner, b *binding, auth AuthorizationView, override parser.InsertOverride) bool {
	hasDomains, hasTypeDefaults, hasDomainInput := false, false, false
	for _, column := range b.Relation.Columns {
		hasDomains = hasDomains || column.Usage.Type.HasDomainConstraints()
		hasTypeDefaults = hasTypeDefaults || typedef.TypeDefault(auth.Catalog, column.Usage.Type) != nil
		hasDomainInput = hasDomainInput || sqltype.ContainsDomainConstraints(column.Usage.Type)
	}
	if len(b.Relation.ColumnMeta.Defaults) == 0 && len(b.Relation.ColumnMeta.Identities) == 0 && !hasDomains && !hasTypeDefaults {
		return hasDomainInput
	}
	b.Insert.Defaults = make([]expr.Expr, len(b.Relation.Columns))
	if hasTypeDefaults {
		b.Insert.DefaultOrigins = make([]defaultOrigin, len(b.Relation.Columns))
	}
	for i, attribute := range b.Relation.Attributes {
		node, origin := columnDefault(auth.Catalog, b.Relation, i)
		b.Insert.Defaults[i] = node
		if b.Insert.DefaultOrigins != nil {
			b.Insert.DefaultOrigins[i] = origin
		}
		if identity, ok := b.Relation.ColumnMeta.Identities[attribute]; ok {
			b.Insert.Defaults[i] = functions.SequenceCall{Info: expr.Info{Usage: b.Relation.Columns[i].Usage, Property: expr.Properties{Volatility: expr.Volatile}}, Name: "nextval", Ref: owner.Domain.Ref(auth.Catalog.Epoch(), identity.Sequence), Identity: true}
		}
	}
	if override == parser.OverrideUser {
		b.Insert.IdentityDiscard = make([]bool, len(b.Relation.Columns))
		for i, attribute := range b.Relation.Attributes {
			_, b.Insert.IdentityDiscard[i] = b.Relation.ColumnMeta.Identities[attribute]
		}
	}
	return hasDomainInput || hasTypeDefaults
}
