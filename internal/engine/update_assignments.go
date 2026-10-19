package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The caller owns namespace and clause ordering; UPDATE, MERGE and conflict
// UPDATE share target, coercion and partial-assignment analysis.
func (e *Engine) bindUpdateAssignments(b *binding, binder *expressionBinder, assignments []parser.UpdateAssignment, u *boundUpdate) error {
	wasUpdateParameters := binder.updateParameters
	binder.updateParameters = true
	defer func() {
		binder.updateParameters = wasUpdateParameters
		binder.updateInputParameters = nil
	}()
	var err error
	for i, a := range assignments {
		if len(a.Steps) != 0 {
			if _, requested := a.Value.(parser.DefaultExpr); requested {
				return expr.ErrorAt("0A000", "cannot set an array element to DEFAULT", a.Target.Pos)
			}
		}
		if slot, exists := b.relation.Index[a.Target.Text]; exists {
			if _, generated := b.relation.ColumnMeta.Generations[b.relation.Attributes[slot]]; generated {
				if _, requested := a.Value.(parser.DefaultExpr); !requested {
					return generationWriteError(a.Target.Text, true)
				}
				continue
			}
		}
		if _, requested := a.Value.(parser.DefaultExpr); requested {
			if b.relation.RelationKind == catalog.ViewRelation {
				continue // Rewrite diagnoses computed/generated destinations.
			}
			return expr.ErrorAt("0A000", "UPDATE DEFAULT is unsupported", a.Value.SourcePosition())
		}
		expected := sqltype.Usage{}
		if slot, ok := b.relation.Index[a.Target.Text]; ok {
			expected = b.relation.Columns[slot].Usage
			if len(a.Steps) != 0 {
				expected = targetUsage(expected, a.Steps)
			}
		}
		u.assignments[i], err = binder.bind(a.Value, expressionPolicy{context: projectionContext, expected: expected, mode: sqltype.Assignment})
		if err != nil {
			return err
		}
	}
	seen := make(map[int]bool, len(assignments))
	for i, a := range assignments {
		slot, ok := b.relation.Index[a.Target.Text]
		if !ok {
			switch a.Target.Text {
			case "ctid", "tableoid", "xmin", "xmax", "cmin", "cmax":
				return expr.ErrorAt("0A000", fmt.Sprintf(`cannot assign to system column "%s"`, a.Target.Text), a.Target.Pos)
			}
			diagnostic := expr.ErrorAt("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Target.Text, b.relation.Name()), a.Target.Pos).(*sqlstate.Error)
			visible := binder.targetName
			if binder.targetAlias != "" {
				visible = binder.targetAlias
			}
			if len(a.Steps) != 0 && a.Steps[0].Field != nil && a.Target.Text == visible {
				diagnostic.Hint = "SET target columns cannot be qualified with the relation name."
			}
			return diagnostic
		}
		partial := len(a.Steps) != 0
		if previous, exists := seen[slot]; exists && (!previous || !partial) {
			return sqlError("42601", fmt.Sprintf(`multiple assignments to same column "%s"`, a.Target.Text))
		}
		seen[slot] = partial
		if _, generated := b.relation.ColumnMeta.Generations[b.relation.Attributes[slot]]; generated {
			b.targets = append(b.targets, slot)
			continue
		}
		if identity, ok := b.relation.ColumnMeta.Identities[b.relation.Attributes[slot]]; ok && identity.Mode == parser.IdentityAlways {
			diagnostic := identityInsertError(a.Target.Text).(*sqlstate.Error)
			diagnostic.Message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, a.Target.Text)
			diagnostic.Hint = ""
			return diagnostic
		}
		b.targets = append(b.targets, slot)
		source := u.assignments[i]
		dest := b.relation.Columns[slot]
		if source == nil && b.relation.RelationKind == catalog.ViewRelation {
			continue
		}
		var target *arrayAssignmentTarget
		if partial {
			target, err = binder.bindAssignmentTarget(dest, a.Steps, a.Target.Pos)
			if err != nil {
				return err
			}
			if u.arrayTargets == nil {
				u.arrayTargets = make([]*arrayAssignmentTarget, len(assignments))
			}
			u.arrayTargets[i] = target
			dest = target.column(dest)
		}
		if source.ResultUsage() != dest.Usage {
			if row, ok := source.(rowExpression); ok && row.Usage.Type.ID() == 2249 && dest.Usage.Type.BaseType().IsComposite() {
				source, err = binder.assignCompositeMember(row, dest.Usage, sqltype.Assignment, a.Value.SourcePosition())
				if err != nil {
					return err
				}
				u.assignments[i] = source
				continue
			}
			input, annotations := domainCoercionInput(source, dest.Usage)
			info, err := storageCoercionInfo(binder.bindingCatalog(), binder.info(dest.Usage, a.Value.SourcePosition(), input.Properties()), input)
			if err != nil {
				return err
			}
			plan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
			if err != nil {
				if target != nil {
					return target.diagnostic(binder, err, source.ResultUsage().Type)
				}
				var d *sqlstate.Error
				if errors.As(err, &d) && d.Code == "42804" {
					pos := parser.ExpressionStart(a.Value)
					if source.ResultUsage().Type.BaseType().IsComposite() {
						pos = source.SourcePosition()
					}
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, dest.Name, binder.diagnosticTypeName(dest.Usage.Type), binder.diagnosticTypeName(source.ResultUsage().Type)), Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(pos.Character)}
				}
				return err
			}
			if plan.RequiresCatalogOutput() {
				binder.regclassConcat = true
			}
			info.Property = coercionProperties(input, plan)
			source = restoreDomainCastCollations(annotations, coercionExpression{info, input, plan, sqltype.Assignment, false})
			source, err = storageDestination(source, dest.Usage, info.Pos)
			if err != nil {
				return err
			}
			u.assignments[i] = source
		}
	}
	b.update = u
	return nil
}

// All RHS expressions read the unchanged action frame. Partial assignments
// compose only in the replacement, never in the frame seen by the next RHS.
func evaluateUpdateAssignments(eval *EvalContext, action *boundUpdate, targets []int, old []sqltype.Value) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(old))
	copy(row, old)
	for i, expr := range action.assignments {
		if expr == nil {
			continue
		}
		target := targets[i]
		var value sqltype.Value
		var err error
		if len(action.arrayTargets) != 0 && action.arrayTargets[i] != nil {
			value, err = action.arrayTargets[i].applyExpression(eval, row[target], expr)
		} else {
			value, err = expr.Eval(eval)
		}
		if err != nil {
			return nil, err
		}
		row[target] = value
	}
	return row, nil
}
