package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func assignmentTargetHasVirtualReads(target *arrayAssignmentTarget) bool {
	for ; target != nil; target = target.Next {
		if target.Attribute == 0 && boundHasVirtualReads(target.Node) {
			return true
		}
	}
	return false
}

func arrayTargetUsage(container sqltype.Usage, subs []parser.ArraySubscript) sqltype.Usage {
	usage := container.BaseUsage()
	for _, s := range subs {
		if s.Slice {
			return usage
		}
	}
	if usage.Type.IsArray() {
		usage.Type = usage.Type.Element()
	}
	return usage
}

func (b *expressionBinder) bindArrayTarget(column catalog.Column, raw []parser.ArraySubscript, pos parser.Position) (*arrayAssignmentTarget, error) {
	usage := column.Usage.BaseUsage()
	if !usage.Type.IsArray() {
		return nil, expr.ErrorAt("42804", fmt.Sprintf("cannot subscript type %s because it does not support subscripting", b.diagnosticTypeName(usage.Type)), pos)
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
	return &arrayAssignmentTarget{Node: subscriptExpression{Subscripts: subs, Slice: slice}, Container: column.Usage, Destination: arrayTargetUsage(column.Usage, raw), Reapply: coercionPlan, Name: column.Name, Pos: pos}, nil
}

func (b *expressionBinder) arrayAssignmentDiagnostic(t *arrayAssignmentTarget, err error, source *sqltype.Descriptor) error {
	if err == nil {
		return nil
	}
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42804" || source == nil {
		return err
	}
	copy := *d
	destinationName, sourceName := b.diagnosticTypeName(t.Destination.Type), b.diagnosticTypeName(source)
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

func preflightArrayTargets(c *EvalContext, targets []*arrayAssignmentTarget) ([]*arrayAssignmentTarget, error) {
	return preflightArrayTargetsWithPlanning(c, targets, false)
}

func preflightArrayTargetsWithPlanning(c *EvalContext, targets []*arrayAssignmentTarget, planConstants bool) ([]*arrayAssignmentTarget, error) {
	var replacements []*arrayAssignmentTarget
	for i, target := range targets {
		if target == nil {
			continue
		}
		next, err := preflightAssignmentTarget(c, target, planConstants)
		if err != nil {
			return nil, err
		}
		if next != target {
			if replacements == nil {
				replacements = append([]*arrayAssignmentTarget(nil), targets...)
			}
			replacements[i] = next
		}
	}
	if replacements == nil {
		return targets, nil
	}
	return replacements, nil
}

func preflightAssignmentTarget(c *EvalContext, target *arrayAssignmentTarget, planConstants bool) (*arrayAssignmentTarget, error) {
	if target == nil {
		return nil, nil
	}
	node, changed, err := mapSubscript(target.Node, func(old expr.Expr) (expr.Expr, bool, error) { return preflightUpdateExpression(c, old, planConstants) })
	if err != nil {
		return nil, err
	}
	next, err := preflightAssignmentTarget(c, target.Next, planConstants)
	if err != nil {
		return nil, err
	}
	if !changed && next == target.Next {
		return target, nil
	}
	copy := *target
	copy.Node, copy.Next = node, next
	return &copy, nil
}

func targetUsage(container sqltype.Usage, steps []parser.TargetStep) sqltype.Usage {
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

func (b *expressionBinder) bindAssignmentTarget(column catalog.Column, steps []parser.TargetStep, pos parser.Position) (*arrayAssignmentTarget, error) {
	return b.bindAssignmentTargetPath(column, steps, pos, pos)
}

// FieldSelect has no expression location in PostgreSQL. Keep that container
// location separate from the assignment head used by coercion diagnostics.
func (b *expressionBinder) bindAssignmentTargetPath(column catalog.Column, steps []parser.TargetStep, pos, containerPos parser.Position) (*arrayAssignmentTarget, error) {
	if field := steps[0].Field; field != nil {
		shape := column.Usage.Type.BaseType()
		if !shape.IsComposite() {
			return nil, expr.ErrorAt("42804", fmt.Sprintf("cannot assign to field %q of column %q because its type %s is not a composite type", field.Text, column.Name, b.diagnosticTypeName(shape)), pos)
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
			target := &arrayAssignmentTarget{Container: column.Usage, Destination: member.Usage, Reapply: reapply, Name: member.Name, Pos: pos, Attribute: member.Attribute}
			if len(steps) > 1 {
				target.Next, err = b.bindAssignmentTargetPath(catalog.Column{Name: member.Name, Usage: member.Usage}, steps[1:], pos, parser.Position{})
				if err != nil {
					return nil, err
				}
				target.Destination = target.Next.Destination
			}
			return target, nil
		}
		return nil, expr.ErrorAt("42703", fmt.Sprintf("cannot assign to field %q of column %q because there is no such column in data type %s", field.Text, column.Name, b.diagnosticTypeName(shape)), pos)
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
	target, err := b.bindArrayTarget(column, raw, containerPos)
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
