package analyze

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The caller owns namespace and clause ordering; UPDATE, MERGE and conflict
// UPDATE share target, coercion and partial-assignment analysis.
func bindUpdateAssignments(b *Binding, binder *Binder, assignments []parser.UpdateAssignment, u *UpdateBinding) error {
	wasUpdateParameters := binder.Params.UpdateParameters
	binder.Params.UpdateParameters = true
	defer func() {
		binder.Params.UpdateParameters = wasUpdateParameters
		binder.Params.updateInputParameters = nil
	}()
	var err error
	assignments, err = prepareUpdateAssignments(b, binder, assignments, u)
	if err != nil {
		return err
	}
	seen := make(map[int]bool, len(assignments))
	for i, a := range assignments {
		slot, ok := b.Relation.Index[a.Target.Text]
		if !ok {
			switch a.Target.Text {
			case "ctid", "tableoid", "xmin", "xmax", "cmin", "cmax":
				return expr.ErrorAt("0A000", fmt.Sprintf(`cannot assign to system column "%s"`, a.Target.Text), a.Target.Pos)
			}
			diagnostic := expr.ErrorAt("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Target.Text, b.Relation.Name()), a.Target.Pos).(*sqlstate.Error)
			visible := binder.Target.Name
			if binder.Target.alias != "" {
				visible = binder.Target.alias
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
		if _, generated := b.Relation.ColumnMeta.Generations[b.Relation.Attributes[slot]]; generated {
			b.Targets = append(b.Targets, slot)
			continue
		}
		if identity, ok := b.Relation.ColumnMeta.Identities[b.Relation.Attributes[slot]]; ok && identity.Mode == parser.IdentityAlways {
			diagnostic := sequence.IdentityInsertError(a.Target.Text).(*sqlstate.Error)
			diagnostic.Message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, a.Target.Text)
			diagnostic.Hint = ""
			return diagnostic
		}
		b.Targets = append(b.Targets, slot)
		source := u.Assignments[i]
		sourcePos, diagnosticPos := a.Value.SourcePosition(), parser.ExpressionStart(a.Value)
		if len(u.Origins) != 0 {
			sourcePos, diagnosticPos = u.Origins[i], u.Origins[i]
		}
		dest := b.Relation.Columns[slot]
		if source == nil && b.Relation.RelationKind == catalog.ViewRelation {
			continue
		}
		var target *plan.ArrayAssignmentTarget
		if partial {
			target, err = binder.BindAssignmentTarget(dest, a.Steps, a.Target.Pos)
			if err != nil {
				return err
			}
			if u.ArrayTargets == nil {
				u.ArrayTargets = make([]*plan.ArrayAssignmentTarget, len(assignments))
			}
			u.ArrayTargets[i] = target
			dest = target.Column(dest)
		}
		if source.ResultUsage() != dest.Usage {
			// Prebound tuple outputs bypass scalar source binding, but still need
			// the same captured destination diagnostic-name authority.
			if err := binder.CaptureTypeDiagnosticInput(dest.Usage.Type); err != nil {
				return err
			}
			if row, ok := source.(plan.RowExpr); ok && row.Usage.Type.ID() == 2249 && dest.Usage.Type.BaseType().IsComposite() {
				source, err = binder.assignCompositeMember(row, dest.Usage, sqltype.Assignment, sourcePos)
				if err != nil {
					return err
				}
				u.Assignments[i] = source
				continue
			}
			input, annotations := DomainCoercionInput(source, dest.Usage)
			info, err := StorageCoercionInfo(binder.BindingCatalog(), binder.Info(dest.Usage, sourcePos, input.Properties()), input)
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
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`column "%s" is of type %s but expression is of type %s`, dest.Name, binder.DiagnosticTypeName(dest.Usage.Type), binder.DiagnosticTypeName(source.ResultUsage().Type)), Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(pos.Character)}
				}
				return err
			}
			if coercionPlan.RequiresCatalogOutput() {
				binder.Prepared.RegclassConcat = true
			}
			info.Property = CoercionProperties(input, coercionPlan)
			source = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
			source, err = StorageDestination(source, dest.Usage, info.Pos)
			if err != nil {
				return err
			}
			u.Assignments[i] = source
		}
	}
	b.Update = u
	if u.DefaultOrigins != nil {
		return binder.CaptureRetainedWriteInput(u.Assignments, nil, nil)
	}
	return nil
}
