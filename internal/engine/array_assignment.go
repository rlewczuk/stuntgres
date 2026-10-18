package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Array target plans retain only analysis state. Every bound and RHS is evaluated
// against the original execution frame, while writes compose into a private row.
type arrayAssignmentTarget struct {
	node        subscriptExpression
	container   sqltype.Usage
	destination sqltype.Usage
	reapply     sqltype.CoercionPlan
	name        string
	pos         parser.Position
	attribute   int32
	next        *arrayAssignmentTarget
}

func assignmentTargetHasVirtualReads(target *arrayAssignmentTarget) bool {
	for ; target != nil; target = target.next {
		if target.attribute == 0 && boundHasVirtualReads(target.node) {
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

func (b *expressionBinder) bindArrayTarget(column ColumnDescriptor, raw []parser.ArraySubscript, pos parser.Position) (*arrayAssignmentTarget, error) {
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
	plan, err := sqltype.PlanCoercion(usage, column.Usage, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	return &arrayAssignmentTarget{node: subscriptExpression{subscripts: subs, slice: slice}, container: column.Usage, destination: arrayTargetUsage(column.Usage, raw), reapply: plan, name: column.Name, pos: pos}, nil
}

func (t *arrayAssignmentTarget) column(original ColumnDescriptor) ColumnDescriptor {
	original.Usage = t.destination
	return original
}
func (t *arrayAssignmentTarget) diagnostic(b *expressionBinder, err error, source *sqltype.Descriptor) error {
	if err == nil {
		return nil
	}
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42804" || source == nil {
		return err
	}
	copy := *d
	destinationName, sourceName := b.diagnosticTypeName(t.destination.Type), b.diagnosticTypeName(source)
	leaf := t
	for leaf.next != nil {
		leaf = leaf.next
	}
	if leaf.attribute != 0 {
		copy.Message = fmt.Sprintf("subfield %q is of type %s but expression is of type %s", leaf.name, destinationName, sourceName)
	} else {
		copy.Message = fmt.Sprintf("subscripted assignment to %q requires type %s but expression is of type %s", t.name, destinationName, sourceName)
	}
	copy.Hint = "You will need to rewrite or cast the expression."
	copy.Position = strconv.Itoa(t.pos.Character)
	return &copy
}

func (t *arrayAssignmentTarget) apply(c *EvalContext, current, rhs sqltype.Value) (sqltype.Value, error) {
	return t.applyPath(c, current, nil, rhs)
}

func (t *arrayAssignmentTarget) applyExpression(c *EvalContext, current sqltype.Value, rhs expr.Expr) (sqltype.Value, error) {
	return t.applyPath(c, current, rhs, sqltype.Value{})
}

func (t *arrayAssignmentTarget) applyPath(c *EvalContext, current sqltype.Value, expression expr.Expr, rhs sqltype.Value) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(c, t.node.subscripts, t.node.slice, storage[:len(t.node.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, bound := range bounds {
		if bound.LowerNull || bound.UpperNull {
			return sqltype.Value{}, sqlError("22004", "array subscript in assignment must not be null")
		}
	}
	if t.next != nil {
		var previous sqltype.Value
		if t.attribute != 0 {
			index, ok := current.Type().BaseType().CompositeAttributeIndex(t.attribute)
			if !ok {
				return sqltype.Value{}, fmt.Errorf("stuntgres: retired assignment attribute")
			}
			previous = sqltype.Null(current.Type().BaseType().CompositeMember(index).Usage.Type)
			if !current.IsNull() {
				previous = current.CompositeField(index)
			}
		} else if t.node.slice {
			previous, err = sqltype.ArraySlice(c.Context, current, bounds)
		} else {
			previous, err = sqltype.ArrayFetch(c.Context, current, bounds)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		rhs, err = t.next.applyPath(c, previous, expression, rhs)
	} else if expression != nil {
		rhs, err = expression.Eval(c)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.applyBounds(c, current, rhs, bounds)
}

func (t *arrayAssignmentTarget) applyBounds(c *EvalContext, current, rhs sqltype.Value, bounds []sqltype.ArraySubscriptValue) (sqltype.Value, error) {
	var result sqltype.Value
	var err error
	if t.attribute != 0 {
		shape := current.Type().BaseType()
		index, ok := shape.CompositeAttributeIndex(t.attribute)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: retired assignment attribute")
		}
		fields := make([]sqltype.Value, shape.CompositeLen())
		for i := range fields {
			fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
			if !current.IsNull() {
				fields[i] = current.CompositeField(i)
			}
		}
		fields[index] = rhs
		result, err = sqltype.OwnedCompositeValue(shape, fields)
		if err != nil {
			return sqltype.Value{}, err
		}
		return t.reapply.Apply(c.Context, result, c.Input, c.Output)
	}
	if t.node.slice {
		result, err = sqltype.ArraySetSlice(c.Context, current, bounds, rhs)
	} else {
		result, err = sqltype.ArraySetElement(c.Context, current, bounds, rhs)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.reapply.Apply(c.Context, result, c.Input, c.Output)
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
	node, changed, err := mapSubscript(target.node, func(old expr.Expr) (expr.Expr, bool, error) { return preflightUpdateExpression(c, old, planConstants) })
	if err != nil {
		return nil, err
	}
	next, err := preflightAssignmentTarget(c, target.next, planConstants)
	if err != nil {
		return nil, err
	}
	if !changed && next == target.next {
		return target, nil
	}
	copy := *target
	copy.node, copy.next = node, next
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

func (b *expressionBinder) bindAssignmentTarget(column ColumnDescriptor, steps []parser.TargetStep, pos parser.Position) (*arrayAssignmentTarget, error) {
	return b.bindAssignmentTargetPath(column, steps, pos, pos)
}

// FieldSelect has no expression location in PostgreSQL. Keep that container
// location separate from the assignment head used by coercion diagnostics.
func (b *expressionBinder) bindAssignmentTargetPath(column ColumnDescriptor, steps []parser.TargetStep, pos, containerPos parser.Position) (*arrayAssignmentTarget, error) {
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
			target := &arrayAssignmentTarget{container: column.Usage, destination: member.Usage, reapply: reapply, name: member.Name, pos: pos, attribute: member.Attribute}
			if len(steps) > 1 {
				target.next, err = b.bindAssignmentTargetPath(ColumnDescriptor{Name: member.Name, Usage: member.Usage}, steps[1:], pos, parser.Position{})
				if err != nil {
					return nil, err
				}
				target.destination = target.next.destination
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
	target.pos = pos
	if count < len(steps) {
		target.next, err = b.bindAssignmentTargetPath(ColumnDescriptor{Name: column.Name, Usage: target.destination}, steps[count:], pos, containerPos)
		if err != nil {
			return nil, err
		}
		target.destination = target.next.destination
	}
	return target, nil
}
