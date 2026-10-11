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
	if len(s.Targets) == 0 && s.Source == nil {
		width := len(s.Rows[0])
		if width > len(b.relation.columns) {
			return insertError("42601", "INSERT has more expressions than target columns", s.Rows[0][len(b.relation.columns)].SourcePosition())
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
		if len(target.Subscripts) == 0 {
			continue
		}
		if binder.mergeInput {
			return expressionError("0A000", "MERGE partial insertion targets are unsupported", target.Pos)
		}
		if b.arrayTargets == nil {
			b.arrayTargets = make([]*arrayAssignmentTarget, len(b.targets))
		}
		partial, err := binder.bindArrayTarget(b.relation.columns[b.targets[i]], target.Subscripts, target.Pos)
		if err != nil {
			return err
		}
		b.arrayTargets[i] = partial
	}
	if len(b.relation.defaults) != 0 || len(b.relation.identities) != 0 {
		b.defaults = make([]BoundExpr, len(b.relation.columns))
		for i, attribute := range b.relation.attributes {
			b.defaults[i] = b.relation.defaults[attribute].expression
		}
		for i, attribute := range b.relation.attributes {
			identity, ok := b.relation.identities[attribute]
			if !ok {
				continue
			}
			b.defaults[i] = sequenceCallExpression{expressionInfo: expressionInfo{usage: b.relation.columns[i].Usage, properties: ExprProperties{Volatility: Volatile}}, name: "nextval", ref: ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: identity.sequence}, identity: true}
		}
		if s.Override == parser.OverrideUser {
			b.identityDiscard = make([]bool, len(b.relation.columns))
			for i, attribute := range b.relation.attributes {
				_, b.identityDiscard[i] = b.relation.identities[attribute]
			}
		}
	}
	if s.Source != nil {
		if err := e.bindInsertSource(b, binder, auth, s); err != nil {
			return err
		}
		return binder.captureRetainedWriteInput(b.defaults, nil)
	}
	binder.aggregateClause = "INSERT VALUES"
	b.expressions = make([][]BoundExpr, len(s.Rows))
	for rowIndex, row := range s.Rows {
		if rowIndex > 0 && len(row) != len(s.Rows[0]) {
			return insertError("42601", "VALUES lists must all be the same length", row[0].SourcePosition())
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
			if stringRow {
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
					return partial.diagnostic(err, source)
				}
				if plan == nil && containsSumAnalysis(v) {
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
		remaining := len(b.relation.defaults) + len(b.relation.identities)
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
	if len(b.relation.identities) != 0 {
		b.insertProjection = make([]int, len(b.relation.columns))
		for i := range b.insertProjection {
			b.insertProjection[i] = -1
		}
		for i, target := range b.targets {
			b.insertProjection[target] = i
		}
	}
	return binder.captureRetainedWriteInput(b.defaults, nil)
}
