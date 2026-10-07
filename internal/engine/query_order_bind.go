package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func orderOrdinal(raw parser.Expr, width int) (int, bool, error) {
	literal, ok := raw.(parser.LiteralExpr)
	if !ok {
		return 0, false, nil
	}
	value := literal.Value
	constant := parser.OrderConstant{
		Value: parser.Value{Kind: parser.NumericValue, Text: value.Text, Pos: value.Pos},
		Real:  value.Kind != parser.IntegerValue,
	}
	if value.Kind == parser.IntegerValue {
		constant.Value.Text = strconv.FormatInt(value.Integer, 10)
		if value.Integer < 0 {
			constant.Sign = "-"
			constant.Value.Text = strings.TrimPrefix(constant.Value.Text, "-")
		}
	}
	ordinal, ok := constant.Ordinal()
	if !ok {
		return 0, false, expressionError("42601", "non-integer constant in ORDER BY", raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expressionError("42P10", fmt.Sprintf("ORDER BY position %d is not in select list", ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}

// ORDER references source-type outputs, before INSERT destination assignment.
func (b *expressionBinder) orderOutput(expression BoundExpr) (BoundExpr, error) {
	if unknown, ok := expression.(unknownOutput); ok {
		var err error
		expression, err = b.bind(unknown.raw, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
	}
	return b.finalizeProjection(expression)
}

func (b *expressionBinder) bindOrder(order *parser.Order, projection []BoundExpr, names []string) ([]sortKey, error) {
	if order == nil {
		return nil, nil
	}
	keys := make([]sortKey, len(order.Keys))
	for i, raw := range order.Keys {
		output, ordinal, err := orderOrdinal(raw.Expr, len(projection))
		if err != nil {
			return nil, err
		}
		if !ordinal {
			output = -1
			if column, ok := raw.Expr.(parser.ColumnExpr); ok && len(column.Name.Parts) == 1 {
				name := column.Name.Parts[0].Text
				for j, label := range names {
					if label != name {
						continue
					}
					projection[j], err = b.orderOutput(projection[j])
					if err != nil {
						return nil, err
					}
					if output >= 0 && !b.sameGroupExpression(projection[output], projection[j]) {
						return nil, expressionError("42702", fmt.Sprintf(`ORDER BY "%s" is ambiguous`, name), raw.Pos)
					}
					output = j
				}
			}
		}
		var expression BoundExpr
		if output >= 0 {
			expression, err = b.orderOutput(projection[output])
		} else {
			expression, err = b.bind(raw.Expr, expressionPolicy{context: projectionContext})
			if err == nil {
				expression, err = b.finalizeProjection(expression)
			}
		}
		if err != nil {
			return nil, err
		}
		if output >= 0 {
			projection[output] = expression
		}
		keys[i] = sortKey{
			expression: expression,
			desc:       raw.Desc,
			nullsFirst: raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc,
			cache:      -1,
		}
	}
	return keys, nil
}

// finishOrder runs after grouped rewriting and output typing. Only referenced
// equivalent visible occurrences share a datum; unrelated outputs are not CSE.
func (b *expressionBinder) finishOrder(root LogicalNode, keys []sortKey, projection []BoundExpr) (LogicalNode, error) {
	if len(keys) == 0 {
		return root, nil
	}
	computed := 0
	for i := range keys {
		key := &keys[i]
		provider, err := sqltype.BindOrdering(key.expression.ResultUsage())
		if err != nil {
			return nil, err
		}
		key.comparison = provider
		key.slot = -1
		switch e := key.expression.(type) {
		case columnExpression:
			key.slot = e.slot
		case groupExpression:
			key.slot = e.slot
		}
		if key.slot >= 0 {
			continue
		}
		visible := false
		for _, output := range projection {
			if b.sameGroupExpression(output, key.expression) {
				visible = true
				break
			}
		}
		if visible {
			for previous := range i {
				if keys[previous].cache >= 0 && b.sameGroupExpression(keys[previous].expression, key.expression) {
					key.cache = keys[previous].cache
					break
				}
			}
		}
		if key.cache < 0 {
			key.cache = computed
			computed++
		}
	}
	for j, output := range projection {
		for _, key := range keys {
			if key.cache >= 0 && b.sameGroupExpression(output, key.expression) {
				properties := output.Properties()
				properties.StageDependent = true
				projection[j] = cachedOrderExpression{
					expressionInfo: expressionInfo{usage: output.ResultUsage(), pos: output.SourcePosition(), properties: properties},
					slot:           key.cache,
				}
				break
			}
		}
	}
	return &sortNode{querySchema: root.Schema(), child: root, keys: keys, cacheWidth: computed}, nil
}

type cachedOrderExpression struct {
	expressionInfo
	slot int
}

func (e cachedOrderExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.slot < 0 || e.slot >= len(c.orderValues) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing ordering output")
	}
	return c.orderValues[e.slot], nil
}
