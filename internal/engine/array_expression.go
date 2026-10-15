package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Constructors retain scalar children and their stored dependencies rather than
// replacing the expression with opaque array input text.
type arrayExpression struct {
	expressionInfo
	elements         []BoundExpr
	multidimensional bool
}

func (n arrayExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	values := make([]sqltype.Value, len(n.elements))
	for i, child := range n.elements {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		v, err := child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return sqltype.ConstructArray(c.Context, n.usage.Type, values, n.multidimensional)
}

// target is supplied only by explicit array casts, never ordinary assignment.
func (b *expressionBinder) bindArray(raw parser.ArrayExpr, target sqltype.Usage) (BoundExpr, error) {
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
		err := expressionError("42P18", "cannot determine type of empty array", raw.Pos).(*sqlstate.Error)
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
				return nil, expressionError("42804", "ARRAY types cannot be matched", raw.Pos)
			}
			target = selected
		} else {
			array, ok := b.definitionArray(selected.Type)
			if !ok || !array.Concrete() {
				return nil, expressionError("0A000", "unsupported array element type", raw.Pos)
			}
			target = selected
			target.Type = array
		}
	}
	childUsage := selected
	if multidimensional {
		childUsage = target
	}
	elements := make([]BoundExpr, len(operands))
	properties := ExprProperties{}
	for i, o := range operands {
		var node BoundExpr
		var err error
		if explicit {
			node, err = b.finalizeOperand(o, childUsage.Type)
			if err == nil && node.ResultUsage() != childUsage {
				input, annotations := domainCoercionInput(node, childUsage)
				var info expressionInfo
				info, err = b.coercionInfo(b.info(childUsage, node.SourcePosition(), input.Properties()), input)
				if err == nil {
					var plan sqltype.CoercionPlan
					plan, err = sqltype.PlanCoercion(input.ResultUsage(), info.usage, sqltype.Explicit)
					if err == nil {
						info.properties = coercionProperties(input, plan)
						node = restoreDomainCastCollations(annotations, coercionExpression{expressionInfo: info, child: input, plan: plan, mode: sqltype.Explicit, syntheticConstructorElement: true})
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
		properties = mergeExpressionProperties(properties, node.Properties())
	}
	info, err := b.collatedInfo(b.info(target, raw.Pos, properties), elements...)
	if err != nil {
		return nil, err
	}
	return arrayExpression{expressionInfo: info, elements: elements, multidimensional: multidimensional}, nil
}

func (r scalarRenderer) arraySQL(ctx context.Context, view CatalogRead, n arrayExpression) (string, error) {
	parts := make([]string, len(n.elements))
	for i, child := range n.elements {
		text, err := r.sql(ctx, view, child)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	text := "ARRAY[" + strings.Join(parts, ", ") + "]"
	if len(parts) == 0 {
		declaration, err := catalogTypeDeclarationAt(view, r.definition, n.usage)
		if err != nil {
			return "", err
		}
		text += "::" + declaration
	}
	return text, nil
}

type boundArraySubscript struct {
	lower, upper BoundExpr
	slice        bool
}
type subscriptExpression struct {
	expressionInfo
	base       BoundExpr
	subscripts []boundArraySubscript
	slice      bool
}

func (n subscriptExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.base.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.usage.Type), nil
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(c, n.subscripts, n.slice, storage[:len(n.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.slice {
		return sqltype.ArraySlice(c.Context, value, bounds)
	}
	return sqltype.ArrayFetch(c.Context, value, bounds)
}

// Executor ordering is upper bounds in dimension order, then lower bounds.
// Analysis and diagnostic positions still follow lexical occurrences.
func evaluateArrayBounds(c *EvalContext, subs []boundArraySubscript, slice bool, bounds []sqltype.ArraySubscriptValue) ([]sqltype.ArraySubscriptValue, error) {
	for i, s := range subs {
		bounds[i] = sqltype.ArraySubscriptValue{}
		if slice && !s.slice {
			bounds[i].Lower, bounds[i].HasLower = 1, true
		}
	}
	for _, lower := range [2]bool{false, true} {
		for i, s := range subs {
			node := s.upper
			if lower {
				node = s.lower
			}
			if node == nil {
				continue
			}
			v, err := node.Eval(c)
			if err != nil {
				return nil, err
			}
			var number int64
			if !v.IsNull() {
				number, err = v.Integer()
				if err != nil {
					return nil, err
				}
			}
			if lower {
				bounds[i].Lower, bounds[i].HasLower, bounds[i].LowerNull = int32(number), true, v.IsNull()
			} else {
				bounds[i].Upper, bounds[i].HasUpper, bounds[i].UpperNull = int32(number), true, v.IsNull()
			}
		}
	}
	return bounds, nil
}

func (b *expressionBinder) bindArrayBounds(raw []parser.ArraySubscript) ([]boundArraySubscript, bool, error) {
	var subs []boundArraySubscript
	if len(raw) <= 6 {
		subs = make([]boundArraySubscript, len(raw))
	}
	slice := false
	bind := func(raw parser.Expr) (BoundExpr, error) {
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
		plan, err := sqltype.PlanCoercion(node.ResultUsage(), b.typ(sqltype.Int4).Usage(), sqltype.Assignment)
		if err != nil {
			return nil, expressionError("42804", "array subscript must have type integer", parser.ExpressionStart(raw))
		}
		return coercionExpression{b.info(b.typ(sqltype.Int4).Usage(), node.SourcePosition(), coercionProperties(node, plan)), node, plan, sqltype.Assignment, false}, nil
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
			subs[i] = boundArraySubscript{lower, upper, s.Slice}
		}
		slice = slice || s.Slice
	}
	if len(raw) > 6 {
		return nil, false, expressionError("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", len(raw)), parser.Position{})
	}
	return subs, slice, nil
}

func (b *expressionBinder) bindSubscript(raw parser.SubscriptExpr) (BoundExpr, error) {
	base, err := b.bind(raw.Base, expressionPolicy{context: projectionContext})
	if err != nil {
		return nil, err
	}
	usage := base.ResultUsage().BaseUsage()
	if !usage.Type.IsArray() {
		return nil, expressionError("42804", fmt.Sprintf("cannot subscript type %s because it does not support subscripting", usage.Type.DiagnosticName()), parser.ExpressionStart(raw.Base))
	}
	subs, slice, err := b.bindArrayBounds(raw.Subscripts)
	if err != nil {
		return nil, err
	}
	properties := base.Properties()
	for _, s := range subs {
		for _, child := range [2]BoundExpr{s.lower, s.upper} {
			if child != nil {
				properties = mergeExpressionProperties(properties, child.Properties())
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
	return subscriptExpression{info, base, subs, slice}, nil
}

func (r scalarRenderer) subscriptSQL(ctx context.Context, view CatalogRead, n subscriptExpression) (string, error) {
	text, err := r.sql(ctx, view, n.base)
	if err != nil {
		return "", err
	}
	text = "(" + text + ")"
	for _, s := range n.subscripts {
		text += "["
		if s.lower != nil {
			part, err := r.sql(ctx, view, s.lower)
			if err != nil {
				return "", err
			}
			text += part
		}
		if s.slice {
			text += ":"
		}
		if s.upper != nil {
			part, err := r.sql(ctx, view, s.upper)
			if err != nil {
				return "", err
			}
			text += part
		}
		text += "]"
	}
	return text, nil
}

func subscriptChildren(n subscriptExpression, storage *[13]BoundExpr) []BoundExpr {
	count := 0
	if n.base != nil {
		storage[count] = n.base
		count++
	}
	for _, s := range n.subscripts {
		if s.lower != nil {
			storage[count] = s.lower
			count++
		}
		if s.upper != nil {
			storage[count] = s.upper
			count++
		}
	}
	return storage[:count]
}

// Only actual changed child lists are copied; shared plans remain immutable.
func mapSubscript(n subscriptExpression, rewrite func(BoundExpr) (BoundExpr, bool, error)) (subscriptExpression, bool, error) {
	base, changed, err := rewrite(n.base)
	if err != nil {
		return n, false, err
	}
	n.base = base
	var subs []boundArraySubscript
	for i, s := range n.subscripts {
		lower, did, err := rewrite(s.lower)
		if err != nil {
			return n, false, err
		}
		upper, more, err := rewrite(s.upper)
		if err != nil {
			return n, false, err
		}
		if did || more {
			if subs == nil {
				subs = append([]boundArraySubscript(nil), n.subscripts...)
			}
			s.lower, s.upper = lower, upper
			subs[i] = s
			changed = true
		}
	}
	if subs != nil {
		n.subscripts = subs
	}
	return n, changed, nil
}
