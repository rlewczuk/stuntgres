package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// callableOperand admits definition-only ARRAY shape for overload lookup.
// Only a selected consumer asks for execution capability; no array node or
// datum is fabricated for a lookup-only companion.
func (b *Binder) callableOperand(raw parser.Expr) (ComparisonOperand, error) {
	if array, ok := raw.(parser.ArrayExpr); ok {
		var usage sqltype.Usage
		node, err := b.bindArray(array, sqltype.Usage{}, &usage)
		return ComparisonOperand{Node: node, raw: raw, lookupUsage: usage}, err
	}
	return b.comparisonOperand(raw)
}

// target is supplied only by explicit array casts, never ordinary assignment.
func (b *Binder) BindArray(raw parser.ArrayExpr, target sqltype.Usage) (expr.Expr, error) {
	return b.bindArray(raw, target, nil)
}

func (b *Binder) bindArray(raw parser.ArrayExpr, target sqltype.Usage, lookupUsage *sqltype.Usage) (expr.Expr, error) {
	operands := make([]ComparisonOperand, len(raw.Elements))
	var selected sqltype.Usage
	explicit := target.Type != nil && target.Type.BaseType().IsArray() && !target.Type.BaseType().IsCatalogVector()
	if explicit {
		target = target.BaseUsage()
		selected = target.Type.Element().Usage()
		selected.Typmod = target.Typmod
		selected.Collation = target.Collation
	}
	multidimensional := false
	for i, child := range raw.Elements {
		if nested, ok := child.(parser.ArrayExpr); ok && explicit {
			node, err := b.BindArray(nested, target)
			if err != nil {
				return nil, err
			}
			operands[i] = ComparisonOperand{Node: node, raw: child}
		} else {
			var o ComparisonOperand
			var err error
			if lookupUsage != nil {
				o, err = b.callableOperand(child)
			} else {
				o, err = b.comparisonOperand(child)
			}
			if err != nil {
				return nil, err
			}
			operands[i] = o
		}
		if d := operands[i].descriptor(); d != nil && d.IsArray() {
			multidimensional = true
		}
	}
	if len(operands) == 0 && !explicit {
		err := expr.ErrorAt("42P18", "cannot determine type of empty array", raw.Pos).(*sqlstate.Error)
		err.Hint = "Explicitly cast to the desired type, for example ARRAY[]::integer[]."
		return nil, err
	}
	if !explicit {
		var err error
		selected, err = CommonExpressionUsage(b.BindingCatalog(), operands, b.Type(sqltype.Text).Usage(), "ARRAY")
		if err != nil {
			return nil, err
		}
		if multidimensional {
			if !selected.Type.IsArray() {
				return nil, expr.ErrorAt("42804", "ARRAY types cannot be matched", raw.Pos)
			}
			target = selected
		} else {
			array, ok := b.definitionArray(selected.Type)
			if selected.Type.ID() == 2249 && selected.Type.IsComposite() {
				var err error
				array, err = sqltype.Array(2287, "_record", selected.Type)
				if err != nil {
					return nil, err
				}
				ok = true
			}
			if !ok {
				return nil, expr.ErrorAt("42704", fmt.Sprintf("could not find array type for data type %s", b.DiagnosticTypeName(selected.Type)), raw.Pos)
			}
			if !array.Concrete() && lookupUsage == nil {
				return nil, expr.ErrorAt("0A000", "unsupported array element type", raw.Pos)
			}
			target = selected
			target.Type = array
		}
	}
	childUsage := selected
	if multidimensional {
		childUsage = target
	}
	deferred := lookupUsage != nil && !target.Type.Concrete()
	elements := make([]expr.Expr, len(operands))
	properties := expr.Properties{}
	for i, o := range operands {
		if o.lookupUsage.Type != nil && deferred && o.usage() == childUsage {
			continue // A nested lookup-only constructor was already checked.
		}
		var node expr.Expr
		var err error
		if explicit {
			if row, structural := o.raw.(parser.RowExpr); structural && childUsage.Type.BaseType().IsComposite() {
				node, err = b.BindRow(row, childUsage, sqltype.Explicit, row.Pos)
			} else {
				node, err = b.finalizeOperand(o, childUsage.Type)
			}
			if err == nil && node.ResultUsage() != childUsage {
				input, annotations := DomainCoercionInput(node, childUsage)
				var info expr.Info
				info, err = b.coercionInfo(b.Info(childUsage, node.SourcePosition(), input.Properties()), input)
				if err == nil {
					if selected, handled, selectionErr := b.declaredCoercion(input, info.Usage, sqltype.Explicit, o.raw.SourcePosition(), o.raw); handled {
						if selectionErr != nil {
							return nil, selectionErr
						}
						elements[i] = RestoreDomainCastCollations(annotations, selected)
						properties = expr.MergeProperties(properties, selected.Properties())
						continue
					}
					var coercionPlan sqltype.CoercionPlan
					coercionPlan, err = sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Explicit)
					if err != nil {
						return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(input.ResultUsage().Type), b.DiagnosticTypeName(info.Usage.Type)), o.raw.SourcePosition())
					}
					if err == nil {
						info.Property = CoercionProperties(input, coercionPlan)
						node = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Explicit, SyntheticConstructorElement: true})
					}
				}
			}
		} else if childUsage.Type.ID() == 2249 && childUsage.Type.IsComposite() && o.Node != nil && o.Node.ResultUsage().Type.IsDeferredRecord() && plan.StructuralNull(o.Node) {
			node = plan.LiteralExpr{Info: b.Info(childUsage, o.Node.SourcePosition(), o.Node.Properties()), Value: sqltype.Null(childUsage.Type)}
		} else if childUsage.Type.ID() == 2249 && childUsage.Type.IsComposite() && o.Node != nil && o.Node.ResultUsage().Type.IsComposite() {
			node, err = b.assignCompositeMember(o.Node, childUsage, sqltype.Implicit, o.Node.SourcePosition())
		} else {
			node, err = b.CoerceConditional(o, childUsage, "ARRAY")
		}
		if err != nil {
			return nil, err
		}
		elements[i] = node
		properties = expr.MergeProperties(properties, node.Properties())
	}
	if deferred {
		*lookupUsage = target
		return nil, nil
	}
	info, err := b.CollatedInfo(b.Info(target, raw.Pos, properties), elements...)
	if err != nil {
		return nil, err
	}
	return plan.ArrayExpr{Info: info, Elements: elements, Multidimensional: multidimensional}, nil
}

func (b *Binder) bindArrayBounds(raw []parser.ArraySubscript) ([]plan.BoundArraySubscript, bool, error) {
	var subs []plan.BoundArraySubscript
	if len(raw) <= 6 {
		subs = make([]plan.BoundArraySubscript, len(raw))
	}
	slice := false
	bind := func(raw parser.Expr) (expr.Expr, error) {
		return b.bindSubscriptOperand(raw, sqltype.Int4, "array subscript must have type integer")
	}
	for i, s := range raw {
		lower, err := bind(s.Lower)
		if err != nil {
			return nil, false, err
		}
		upper, err := bind(s.Upper)
		if err != nil {
			return nil, false, err
		}
		if subs != nil {
			subs[i] = plan.BoundArraySubscript{Lower: lower, Upper: upper, Slice: s.Slice}
		}
		slice = slice || s.Slice
	}
	if len(raw) > 6 {
		return nil, false, expr.ErrorAt("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", len(raw)), parser.Position{})
	}
	return subs, slice, nil
}

// bindSubscriptOperand binds one subscript and assignment-coerces it to the
// handler's subscript type, failing with message at the operand.
func (b *Binder) bindSubscriptOperand(raw parser.Expr, target sqltype.ID, message string) (expr.Expr, error) {
	if raw == nil {
		return nil, nil
	}
	o, err := b.comparisonOperand(raw)
	if err != nil {
		return nil, err
	}
	typ := b.Type(target)
	node, err := b.finalizeOperand(o, typ)
	if err != nil {
		return nil, err
	}
	if node.ResultUsage().Type == typ {
		return node, nil
	}
	if selected, handled, selectionErr := b.declaredCoercion(node, typ.Usage(), sqltype.Assignment, node.SourcePosition(), raw); handled {
		return selected, selectionErr
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), typ.Usage(), sqltype.Assignment)
	if err != nil {
		return nil, expr.ErrorAt("42804", message, parser.ExpressionStart(raw))
	}
	return plan.CoercionExpr{Info: b.Info(typ.Usage(), node.SourcePosition(), CoercionProperties(node, coercionPlan)), Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}, nil
}

func (b *Binder) BindSubscript(raw parser.SubscriptExpr) (expr.Expr, error) {
	base, err := b.Bind(raw.Base, ExpressionPolicy{Context: ProjectionContext})
	if err != nil {
		return nil, err
	}
	usage := base.ResultUsage().BaseUsage()
	if usage.Type.IsCatalogVector() {
		lowered := b.Type(usage.Type.Element().AssociatedArray()).Usage()
		conversion, err := sqltype.PlanCoercion(usage, lowered, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		base = plan.CoercionExpr{Info: b.Info(lowered, base.SourcePosition(), CoercionProperties(base, conversion)), Child: base, Plan: conversion, Mode: sqltype.Implicit}
		usage = lowered
	}
	if usage.Type.IsHstore() {
		return b.bindHstoreSubscript(raw, base, usage.Type)
	}
	if !usage.Type.IsArray() && usage.Type.ID() != sqltype.Box && usage.Type.ID() != sqltype.Lseg && usage.Type.ID() != sqltype.Point {
		return nil, expr.ErrorAt("42804", fmt.Sprintf("cannot subscript type %s because it does not support subscripting", usage.Type.DiagnosticName()), parser.ExpressionStart(raw.Base))
	}
	subs, slice, err := b.bindArrayBounds(raw.Subscripts)
	if err != nil {
		return nil, err
	}
	properties := base.Properties()
	for _, s := range subs {
		for _, child := range [2]expr.Expr{s.Lower, s.Upper} {
			if child != nil {
				properties = expr.MergeProperties(properties, child.Properties())
			}
		}
	}
	if !slice {
		usage.Type = usage.Type.SubscriptElement()
	}
	info, err := b.CollatedInfo(b.Info(usage, raw.Pos, properties), base)
	if err != nil {
		return nil, err
	}
	return plan.SubscriptExpr{Info: info, Base: base, Subscripts: subs, Slice: slice}, nil
}
