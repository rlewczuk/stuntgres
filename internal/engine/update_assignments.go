package engine

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The caller owns namespace and clause ordering; UPDATE, MERGE and conflict
// UPDATE share target, coercion and partial-assignment analysis.
func bindUpdateAssignments(b *binding, binder *expressionBinder, assignments []parser.UpdateAssignment, u *boundUpdate) error {
	wasUpdateParameters := binder.params.updateParameters
	binder.params.updateParameters = true
	defer func() {
		binder.params.updateParameters = wasUpdateParameters
		binder.params.updateInputParameters = nil
	}()
	var err error
	assignments, err = prepareUpdateAssignments(b, binder, assignments, u)
	if err != nil {
		return err
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
			visible := binder.target.name
			if binder.target.alias != "" {
				visible = binder.target.alias
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
			diagnostic := sequence.IdentityInsertError(a.Target.Text).(*sqlstate.Error)
			diagnostic.Message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, a.Target.Text)
			diagnostic.Hint = ""
			return diagnostic
		}
		b.targets = append(b.targets, slot)
		source := u.assignments[i]
		sourcePos, diagnosticPos := a.Value.SourcePosition(), parser.ExpressionStart(a.Value)
		if len(u.origins) != 0 {
			sourcePos, diagnosticPos = u.origins[i], u.origins[i]
		}
		dest := b.relation.Columns[slot]
		if source == nil && b.relation.RelationKind == catalog.ViewRelation {
			continue
		}
		var target *plan.ArrayAssignmentTarget
		if partial {
			target, err = binder.bindAssignmentTarget(dest, a.Steps, a.Target.Pos)
			if err != nil {
				return err
			}
			if u.arrayTargets == nil {
				u.arrayTargets = make([]*plan.ArrayAssignmentTarget, len(assignments))
			}
			u.arrayTargets[i] = target
			dest = target.Column(dest)
		}
		if source.ResultUsage() != dest.Usage {
			// Prebound tuple outputs bypass scalar source binding, but still need
			// the same captured destination diagnostic-name authority.
			if err := binder.captureTypeDiagnosticInput(dest.Usage.Type); err != nil {
				return err
			}
			if row, ok := source.(plan.RowExpr); ok && row.Usage.Type.ID() == 2249 && dest.Usage.Type.BaseType().IsComposite() {
				source, err = binder.assignCompositeMember(row, dest.Usage, sqltype.Assignment, sourcePos)
				if err != nil {
					return err
				}
				u.assignments[i] = source
				continue
			}
			input, annotations := domainCoercionInput(source, dest.Usage)
			info, err := storageCoercionInfo(binder.bindingCatalog(), binder.info(dest.Usage, sourcePos, input.Properties()), input)
			if err != nil {
				return err
			}
			coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
			if err != nil {
				if target != nil {
					return binder.arrayAssignmentDiagnostic(target, err, source.ResultUsage().Type)
				}
				var d *sqlstate.Error
				if errors.As(err, &d) && d.Code == "42804" {
					pos := diagnosticPos
					if source.ResultUsage().Type.BaseType().IsComposite() {
						pos = source.SourcePosition()
					}
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, dest.Name, binder.diagnosticTypeName(dest.Usage.Type), binder.diagnosticTypeName(source.ResultUsage().Type)), Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(pos.Character)}
				}
				return err
			}
			if coercionPlan.RequiresCatalogOutput() {
				binder.prepared.regclassConcat = true
			}
			info.Property = coercionProperties(input, coercionPlan)
			source = restoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
			source, err = storageDestination(source, dest.Usage, info.Pos)
			if err != nil {
				return err
			}
			u.assignments[i] = source
		}
	}
	b.update = u
	if u.defaultOrigins != nil {
		return binder.captureRetainedWriteInput(u.assignments, nil, nil)
	}
	return nil
}

// All RHS expressions read the unchanged action frame. Partial assignments
// compose only in the replacement, never in the frame seen by the next RHS.
func evaluateUpdateAssignments(eval *exec.Context, action *boundUpdate, targets []int, old []sqltype.Value) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(old))
	copy(row, old)
	oldOutputs, oldReady := eval.UpdateOutputs, eval.UpdateOutputsReady
	if action.outputWidth != 0 {
		if oldOutputs != nil {
			// A nested mutation owner must not overwrite the caller's live tuple.
			previousScratch := eval.UpdateOutputScratch
			var small [4]sqltype.Value
			if action.outputWidth <= len(small) {
				eval.UpdateOutputScratch = small[:action.outputWidth]
			} else {
				eval.UpdateOutputScratch = make([]sqltype.Value, action.outputWidth)
			}
			defer func() { eval.UpdateOutputScratch = previousScratch }()
		}
		if cap(eval.UpdateOutputScratch) < action.outputWidth {
			eval.UpdateOutputScratch = make([]sqltype.Value, action.outputWidth)
		}
		defer func() { eval.UpdateOutputs, eval.UpdateOutputsReady = oldOutputs, oldReady }()
	}
	group := 0
	for i, expr := range action.assignments {
		if group < len(action.groups) && action.groups[group].start == i {
			eval.UpdateOutputs = eval.UpdateOutputScratch[:action.groups[group].width]
			eval.UpdateOutputsReady = false
			group++
		}
		if expr == nil {
			continue
		}
		target := targets[i]
		var value sqltype.Value
		var err error
		if len(action.arrayTargets) != 0 && action.arrayTargets[i] != nil {
			value, err = action.arrayTargets[i].ApplyExpression(eval, row[target], expr)
		} else if len(action.defaultOrigins) != 0 && action.defaultOrigins[i].requested {
			value, err = evalDefaultExpression(eval, expr, action.defaultOrigins[i])
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
