package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Preserve the scalar children in stored expressions. ARRAY construction has
// scalar-constant dependencies; an opaque array input literal does not.
type regclassArrayExpression struct {
	expressionInfo
	elements []BoundExpr
}

func (n regclassArrayExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	values := make([]sqltype.Value, len(n.elements))
	for i, element := range n.elements {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		value, err := element.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	var dimensions []sqltype.ArrayDimension
	if len(values) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
	}
	return sqltype.OwnedArrayValue(n.usage.Type, dimensions, values)
}

func (b *expressionBinder) bindRegclassArray(raw parser.ArrayExpr, expected sqltype.Usage) (BoundExpr, error) {
	typ := b.typ(sqltype.Regclass)
	contextual := expected.Type != nil && expected.Type.BaseType().IsArray() && expected.Type.BaseType().Element() == typ
	elements := make([]BoundExpr, len(raw.Elements))
	selected := contextual
	excluded := func() error {
		spelling := raw.Spelling
		if spelling == "" {
			spelling = "ARRAY"
		}
		return expressionError("42601", fmt.Sprintf("syntax error at or near %q", spelling), raw.Pos)
	}
	for i, element := range raw.Elements {
		if literal, ok := element.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			continue
		}
		node, err := b.bind(element, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if parameter, ok := node.(parameterExpression); ok && parameter.ResultUsage().Type == nil {
			elements[i] = parameter
			continue
		}
		if node.ResultUsage().Type != typ {
			return nil, excluded()
		}
		elements[i] = node
		selected = true
	}
	if !selected {
		if len(elements) == 0 {
			d := expressionError("42P18", "cannot determine type of empty array", raw.Pos).(*sqlstate.Error)
			d.Hint = "Explicitly cast to the desired type, for example ARRAY[]::integer[]."
			return nil, d
		}
		return nil, excluded()
	}
	properties := ExprProperties{}
	for i, element := range raw.Elements {
		if elements[i] == nil {
			node, err := b.bind(element, expressionPolicy{context: projectionContext, expected: typ.Usage()})
			if err != nil {
				return nil, err
			}
			elements[i] = node
		} else if parameter, ok := elements[i].(parameterExpression); ok && parameter.ResultUsage().Type == nil {
			node, err := b.coerceParameter(parameter, typ.Usage())
			if err != nil {
				return nil, err
			}
			elements[i] = node
		}
		properties = mergeExpressionProperties(properties, elements[i].Properties())
	}
	array := b.typ(typ.AssociatedArray())
	if array == nil {
		return nil, fmt.Errorf("stuntgres: missing regclass array descriptor")
	}
	return regclassArrayExpression{b.info(array.Usage(), raw.Pos, properties), elements}, nil
}

func (r scalarRenderer) regclassArraySQL(ctx context.Context, view CatalogRead, n regclassArrayExpression) (string, error) {
	parts := make([]string, len(n.elements))
	for i, element := range n.elements {
		text, err := r.sql(ctx, view, element)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	text := "ARRAY[" + strings.Join(parts, ", ") + "]"
	if len(parts) == 0 {
		text += "::pg_catalog.regclass[]"
	}
	return text, nil
}
