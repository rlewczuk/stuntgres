package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// target is supplied only by explicit array casts, never ordinary assignment.
func (b *expressionBinder) bindArray(raw parser.ArrayExpr, target sqltype.Usage) (expr.Expr, error) {
	operands := make([]comparisonOperand, len(raw.Elements))
	var selected sqltype.Usage
	explicit := target.Type != nil && target.Type.BaseType().IsArray()
	if explicit {
		target = target.BaseUsage()
		selected = target.Type.Element().Usage()
		selected.Typmod = target.Typmod
		selected.Collation = target.Collation
	}
	multidimensional := false
	for i, child := range raw.Elements {
		if nested, ok := child.(parser.ArrayExpr); ok && explicit {
			node, err := b.bindArray(nested, target)
			if err != nil {
				return nil, err
			}
			operands[i] = comparisonOperand{node: node, raw: child}
		} else {
			o, err := b.comparisonOperand(child)
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
		selected, err = commonExpressionUsage(operands, b.typ(sqltype.Text).Usage(), "ARRAY")
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
			if !ok || !array.Concrete() {
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
	elements := make([]expr.Expr, len(operands))
	properties := expr.Properties{}
	for i, o := range operands {
		var node expr.Expr
		var err error
		if explicit {
			node, err = b.finalizeOperand(o, childUsage.Type)
			if err == nil && node.ResultUsage() != childUsage {
				input, annotations := domainCoercionInput(node, childUsage)
				var info expr.Info
				info, err = b.coercionInfo(b.info(childUsage, node.SourcePosition(), input.Properties()), input)
				if err == nil {
					var coercionPlan sqltype.CoercionPlan
					coercionPlan, err = sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Explicit)
					if err == nil {
						info.Property = coercionProperties(input, coercionPlan)
						node = restoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Explicit, SyntheticConstructorElement: true})
					}
				}
			}
		} else {
			node, err = b.coerceConditional(o, childUsage, "ARRAY")
		}
		if err != nil {
			return nil, err
		}
		elements[i] = node
		properties = expr.MergeProperties(properties, node.Properties())
	}
	info, err := b.collatedInfo(b.info(target, raw.Pos, properties), elements...)
	if err != nil {
		return nil, err
	}
	return plan.ArrayExpr{Info: info, Elements: elements, Multidimensional: multidimensional}, nil
}

func (r scalarRenderer) arraySQL(ctx context.Context, view catalog.Read, n plan.ArrayExpr) (string, error) {
	parts := make([]string, len(n.Elements))
	for i, child := range n.Elements {
		text, err := r.sql(ctx, view, child)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	text := "ARRAY[" + strings.Join(parts, ", ") + "]"
	if len(parts) == 0 {
		declaration, err := catalogTypeDeclarationAt(view, r.definition, n.Usage)
		if err != nil {
			return "", err
		}
		text += "::" + declaration
	}
	return text, nil
}

func (b *expressionBinder) bindArrayBounds(raw []parser.ArraySubscript) ([]plan.BoundArraySubscript, bool, error) {
	var subs []plan.BoundArraySubscript
	if len(raw) <= 6 {
		subs = make([]plan.BoundArraySubscript, len(raw))
	}
	slice := false
	bind := func(raw parser.Expr) (expr.Expr, error) {
		if raw == nil {
			return nil, nil
		}
		o, err := b.comparisonOperand(raw)
		if err != nil {
			return nil, err
		}
		node, err := b.finalizeOperand(o, b.typ(sqltype.Int4))
		if err != nil {
			return nil, err
		}
		if node.ResultUsage().Type == b.typ(sqltype.Int4) {
			return node, nil
		}
		coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), b.typ(sqltype.Int4).Usage(), sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", "array subscript must have type integer", parser.ExpressionStart(raw))
		}
		return plan.CoercionExpr{b.info(b.typ(sqltype.Int4).Usage(), node.SourcePosition(), coercionProperties(node, coercionPlan)), node, coercionPlan, sqltype.Assignment, false}, nil
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
			subs[i] = plan.BoundArraySubscript{lower, upper, s.Slice}
		}
		slice = slice || s.Slice
	}
	if len(raw) > 6 {
		return nil, false, expr.ErrorAt("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", len(raw)), parser.Position{})
	}
	return subs, slice, nil
}

func (b *expressionBinder) bindSubscript(raw parser.SubscriptExpr) (expr.Expr, error) {
	base, err := b.bind(raw.Base, expressionPolicy{context: projectionContext})
	if err != nil {
		return nil, err
	}
	usage := base.ResultUsage().BaseUsage()
	if !usage.Type.IsArray() {
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
		usage.Type = usage.Type.Element()
	}
	info, err := b.collatedInfo(b.info(usage, raw.Pos, properties), base)
	if err != nil {
		return nil, err
	}
	return plan.SubscriptExpr{info, base, subs, slice}, nil
}

func (r scalarRenderer) subscriptSQL(ctx context.Context, view catalog.Read, n plan.SubscriptExpr) (string, error) {
	text, err := r.sql(ctx, view, n.Base)
	if err != nil {
		return "", err
	}
	text = "(" + text + ")"
	for _, s := range n.Subscripts {
		text += "["
		if s.Lower != nil {
			part, err := r.sql(ctx, view, s.Lower)
			if err != nil {
				return "", err
			}
			text += part
		}
		if s.Slice {
			text += ":"
		}
		if s.Upper != nil {
			part, err := r.sql(ctx, view, s.Upper)
			if err != nil {
				return "", err
			}
			text += part
		}
		text += "]"
	}
	return text, nil
}

func subscriptChildren(n plan.SubscriptExpr, storage *[13]expr.Expr) []expr.Expr {
	count := 0
	if n.Base != nil {
		storage[count] = n.Base
		count++
	}
	for _, s := range n.Subscripts {
		if s.Lower != nil {
			storage[count] = s.Lower
			count++
		}
		if s.Upper != nil {
			storage[count] = s.Upper
			count++
		}
	}
	return storage[:count]
}

// Only actual changed child lists are copied; shared plans remain immutable.
func mapSubscript(n plan.SubscriptExpr, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.SubscriptExpr, bool, error) {
	base, changed, err := rewrite(n.Base)
	if err != nil {
		return n, false, err
	}
	n.Base = base
	var subs []plan.BoundArraySubscript
	for i, s := range n.Subscripts {
		lower, did, err := rewrite(s.Lower)
		if err != nil {
			return n, false, err
		}
		upper, more, err := rewrite(s.Upper)
		if err != nil {
			return n, false, err
		}
		if did || more {
			if subs == nil {
				subs = append([]plan.BoundArraySubscript(nil), n.Subscripts...)
			}
			s.Lower, s.Upper = lower, upper
			subs[i] = s
			changed = true
		}
	}
	if subs != nil {
		n.Subscripts = subs
	}
	return n, changed, nil
}
