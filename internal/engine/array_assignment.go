package engine

import (
	"fmt"
	"strconv"

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

func (b *expressionBinder) bindArrayTarget(column ColumnDescriptor, raw []parser.ArraySubscript, pos parser.Position, read bool) (*arrayAssignmentTarget, error) {
	usage := column.Usage.BaseUsage()
	if !usage.Type.IsArray() {
		return nil, expressionError("42804", fmt.Sprintf("cannot subscript type %s because it does not support subscripting", usage.Type.DiagnosticName()), pos)
	}
	subs, slice, err := b.bindArrayBounds(raw)
	if err != nil {
		return nil, err
	}
	var base BoundExpr
	if read {
		base, err = b.column(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: column.Name, Pos: pos}}}, Pos: pos})
		if err != nil {
			return nil, err
		}
	}
	plan, err := sqltype.PlanCoercion(usage, column.Usage, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	return &arrayAssignmentTarget{node: subscriptExpression{base: base, subscripts: subs, slice: slice}, container: column.Usage, destination: arrayTargetUsage(column.Usage, raw), reapply: plan, name: column.Name, pos: pos}, nil
}

func (t *arrayAssignmentTarget) column(original ColumnDescriptor) ColumnDescriptor {
	original.Usage = t.destination
	return original
}
func (t *arrayAssignmentTarget) diagnostic(err error, source *sqltype.Descriptor) error {
	if err == nil {
		return nil
	}
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42804" || source == nil {
		return err
	}
	copy := *d
	copy.Message = fmt.Sprintf("subscripted assignment to %q requires type %s but expression is of type %s", t.name, t.destination.Type.DiagnosticName(), source.DiagnosticName())
	copy.Hint = "You will need to rewrite or cast the expression."
	copy.Position = strconv.Itoa(t.pos.Character)
	return &copy
}

func (t *arrayAssignmentTarget) apply(c *EvalContext, current, rhs sqltype.Value) (sqltype.Value, error) {
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(c, t.node.subscripts, t.node.slice, storage[:len(t.node.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.applyBounds(c, current, rhs, bounds)
}

func (t *arrayAssignmentTarget) applyExpression(c *EvalContext, current sqltype.Value, rhs BoundExpr) (sqltype.Value, error) {
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(c, t.node.subscripts, t.node.slice, storage[:len(t.node.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, b := range bounds {
		if b.LowerNull || b.UpperNull {
			return sqltype.Value{}, sqlError("22004", "array subscript in assignment must not be null")
		}
	}
	value, err := rhs.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.applyBounds(c, current, value, bounds)
}

func (t *arrayAssignmentTarget) applyBounds(c *EvalContext, current, rhs sqltype.Value, bounds []sqltype.ArraySubscriptValue) (sqltype.Value, error) {
	var result sqltype.Value
	var err error
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
	var replacements []*arrayAssignmentTarget
	for i, target := range targets {
		if target == nil {
			continue
		}
		node, changed, err := mapSubscript(target.node, func(old BoundExpr) (BoundExpr, bool, error) { return preflightCoercions(c, old) })
		if err != nil {
			return nil, err
		}
		if changed {
			if replacements == nil {
				replacements = append([]*arrayAssignmentTarget(nil), targets...)
			}
			copy := *target
			copy.node = node
			replacements[i] = &copy
		}
	}
	if replacements == nil {
		return targets, nil
	}
	return replacements, nil
}
