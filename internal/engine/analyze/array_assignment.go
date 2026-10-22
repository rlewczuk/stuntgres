package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayTargetUsage(container sqltype.Usage, subs []parser.ArraySubscript) sqltype.Usage {
	usage := container.BaseUsage()
	for _, s := range subs {
		if s.Slice {
			return usage
		}
	}
	if usage.Type.IsArray() || usage.Type.ID() == sqltype.Box {
		usage.Type = usage.Type.SubscriptElement()
	}
	return usage
}

func (b *Binder) BindArrayTarget(column catalog.Column, raw []parser.ArraySubscript, pos parser.Position) (*plan.ArrayAssignmentTarget, error) {
	usage := column.Usage.BaseUsage()
	if !usage.Type.IsArray() && usage.Type.ID() != sqltype.Box {
		return nil, expr.ErrorAt("42804", fmt.Sprintf("cannot subscript type %s because it does not support subscripting", b.DiagnosticTypeName(usage.Type)), pos)
	}
	subs, slice, err := b.bindArrayBounds(raw)
	if err != nil {
		return nil, err
	}
	// The executor supplies the old container for partial UPDATE assignments.
	// Like PostgreSQL's implicit assignment Var, it is not a SELECT reference;
	// only explicit RHS and subscript expressions contribute read authority.
	coercionPlan, err := sqltype.PlanCoercion(usage, column.Usage, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	return &plan.ArrayAssignmentTarget{Node: plan.SubscriptExpr{Subscripts: subs, Slice: slice}, Container: column.Usage, Destination: arrayTargetUsage(column.Usage, raw), Reapply: coercionPlan, Name: column.Name, Pos: pos}, nil
}

func (b *Binder) arrayAssignmentDiagnostic(t *plan.ArrayAssignmentTarget, err error, source *sqltype.Descriptor) error {
	if err == nil {
		return nil
	}
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42804" || source == nil {
		return err
	}
	copy := *d
	destinationName, sourceName := b.DiagnosticTypeName(t.Destination.Type), b.DiagnosticTypeName(source)
	leaf := t
	for leaf.Next != nil {
		leaf = leaf.Next
	}
	if leaf.Attribute != 0 {
		copy.Message = fmt.Sprintf("subfield %q is of type %s but expression is of type %s", leaf.Name, destinationName, sourceName)
	} else {
		copy.Message = fmt.Sprintf("subscripted assignment to %q requires type %s but expression is of type %s", t.Name, destinationName, sourceName)
	}
	copy.Hint = "You will need to rewrite or cast the expression."
	copy.Position = strconv.Itoa(t.Pos.Character)
	return &copy
}

func TargetUsage(container sqltype.Usage, steps []parser.TargetStep) sqltype.Usage {
	usage := container
	for i := 0; i < len(steps); {
		if field := steps[i].Field; field != nil {
			shape := usage.Type.BaseType()
			if !shape.IsComposite() {
				return sqltype.Usage{}
			}
			found := false
			for j := range shape.CompositeLen() {
				member := shape.CompositeMember(j)
				if member.Name == field.Text {
					usage = member.Usage
					found = true
					break
				}
			}
			if !found {
				return sqltype.Usage{}
			}
			i++
		} else {
			var subs [6]parser.ArraySubscript
			n := 0
			for i < len(steps) && steps[i].Subscript != nil {
				if n == len(subs) {
					return sqltype.Usage{}
				}
				subs[n] = *steps[i].Subscript
				n++
				i++
			}
			usage = arrayTargetUsage(usage, subs[:n])
		}
	}
	return usage
}

func (b *Binder) BindAssignmentTarget(column catalog.Column, steps []parser.TargetStep, pos parser.Position) (*plan.ArrayAssignmentTarget, error) {
	return b.bindAssignmentTargetPath(column, steps, pos, pos)
}

// FieldSelect has no expression location in PostgreSQL. Keep that container
// location separate from the assignment head used by coercion diagnostics.
func (b *Binder) bindAssignmentTargetPath(column catalog.Column, steps []parser.TargetStep, pos, containerPos parser.Position) (*plan.ArrayAssignmentTarget, error) {
	if field := steps[0].Field; field != nil {
		shape := column.Usage.Type.BaseType()
		if !shape.IsComposite() {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("cannot assign to field %q of column %q because its type %s is not a composite type", field.Text, column.Name, b.DiagnosticTypeName(shape)), pos)
		}
		for i := range shape.CompositeLen() {
			member := shape.CompositeMember(i)
			if member.Name != field.Text {
				continue
			}
			reapply, err := sqltype.PlanCoercion(column.Usage.BaseUsage(), column.Usage, sqltype.Assignment)
			if err != nil {
				return nil, err
			}
			target := &plan.ArrayAssignmentTarget{Container: column.Usage, Destination: member.Usage, Reapply: reapply, Name: member.Name, Pos: pos, Attribute: member.Attribute}
			if len(steps) > 1 {
				target.Next, err = b.bindAssignmentTargetPath(catalog.Column{Name: member.Name, Usage: member.Usage}, steps[1:], pos, parser.Position{})
				if err != nil {
					return nil, err
				}
				target.Destination = target.Next.Destination
			}
			return target, nil
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf("cannot assign to field %q of column %q because there is no such column in data type %s", field.Text, column.Name, b.DiagnosticTypeName(shape)), pos)
	}
	var storage [6]parser.ArraySubscript
	count := 0
	for count < len(steps) && steps[count].Subscript != nil {
		count++
	}
	raw := storage[:min(count, len(storage))]
	if count > len(storage) {
		raw = make([]parser.ArraySubscript, count)
	}
	for i := range count {
		raw[i] = *steps[i].Subscript
	}
	target, err := b.BindArrayTarget(column, raw, containerPos)
	if err != nil {
		return nil, err
	}
	target.Pos = pos
	if count < len(steps) {
		target.Next, err = b.bindAssignmentTargetPath(catalog.Column{Name: column.Name, Usage: target.Destination}, steps[count:], pos, containerPos)
		if err != nil {
			return nil, err
		}
		target.Destination = target.Next.Destination
	}
	return target, nil
}
