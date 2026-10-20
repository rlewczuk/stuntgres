package engine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func orderOrdinal(raw parser.Expr, width int, clause string) (int, bool, error) {
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
		return 0, false, expr.ErrorAt("42601", "non-integer constant in "+clause, raw.SourcePosition())
	}
	if ordinal < 1 || ordinal > int64(width) {
		return 0, false, expr.ErrorAt("42P10", fmt.Sprintf("%s position %d is not in select list", clause, ordinal), raw.SourcePosition())
	}
	return int(ordinal) - 1, true, nil
}

// ORDER references source-type outputs, before INSERT destination assignment.
func (b *expressionBinder) orderOutput(expression expr.Expr) (expr.Expr, error) {
	if unknown, ok := expression.(plan.UnknownOutput); ok {
		var err error
		expression, err = b.bind(unknown.Raw, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
	}
	return b.finalizeProjection(expression)
}

func (b *expressionBinder) bindOrder(order *parser.Order, projection []expr.Expr, names []string, clause string) ([]plan.SortKey, error) {
	if order == nil {
		return nil, nil
	}
	keys := make([]plan.SortKey, len(order.Keys))
	for i, raw := range order.Keys {
		output, ordinal, err := orderOrdinal(raw.Expr, len(projection), clause)
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
					if output >= 0 && !b.sameQueryTarget(projection[output], projection[j]) {
						return nil, expr.ErrorAt("42702", fmt.Sprintf(`%s "%s" is ambiguous`, clause, name), raw.Pos)
					}
					if output < 0 {
						output = j
					}
				}
			}
		}
		var expression expr.Expr
		if output >= 0 {
			expression, err = b.orderOutput(projection[output])
		} else {
			firstWindowCall := len(b.grouping.windowCalls)
			expression, err = b.bind(raw.Expr, expressionPolicy{context: projectionContext})
			if err == nil {
				expression, err = b.finalizeProjection(expression)
			}
			if err == nil {
				for j, candidate := range projection {
					if b.sameGroupExpression(expression, candidate) {
						output = j
						expression = candidate
						for k := firstWindowCall; k < len(b.grouping.windowCalls); k++ {
							b.grouping.windowCalls[k].AnalysisOnly = true
						}
						break
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if expression.CollationIndeterminate() {
			left, right, position := expression.ImplicitCollationConflict()
			d := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
			d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
			return nil, d
		}
		if output >= 0 {
			projection[output] = expression
		}
		if demand := b.query.namespace.identityValuesOrderDemand; len(demand) != 0 {
			if column, ok := expression.(plan.ColumnExpr); ok {
				demand[column.Slot] = true
			}
		}
		keys[i] = plan.SortKey{
			Expression:    expression,
			Position:      raw.Pos.Character,
			OutputOrdinal: output + 1,
			Desc:          raw.Desc,
			NullsFirst:    raw.Nulls == parser.NullsFirst || raw.Nulls == parser.NullsDefault && raw.Desc,
			Cache:         -1,
		}
	}
	return keys, nil
}

// finishOrder runs after grouped rewriting and output typing. Equivalent ORDER
// targets share a datum; unrelated visible outputs remain occurrence-sensitive.
func (b *expressionBinder) finishOrder(root plan.Node, keys []plan.SortKey, projection []expr.Expr) (plan.Node, error) {
	if len(keys) == 0 {
		return root, nil
	}
	computed := 0
	for i := range keys {
		key := &keys[i]
		if key.TargetIdentity == 0 && key.OutputOrdinal > 0 {
			key.TargetIdentity = key.OutputOrdinal
		}
		if !key.Expression.CollationIndeterminate() && !key.Comparison.OrderingBound() {
			provider, err := sqltype.BindOrdering(key.Expression.ResultUsage())
			if err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
					positioned := *diagnostic
					if key.Position > 0 {
						positioned.Position = strconv.Itoa(key.Position)
					}
					return nil, &positioned
				}
				return nil, err
			}
			key.Comparison = provider
		}
		// Indeterminate set collations reject during execution preflight.
		// They still participate in the ordinary output/cache equivalence.
		key.Slot = -1
		switch e := key.Expression.(type) {
		case plan.ColumnExpr:
			if e.Virtual == nil {
				key.Slot = e.Slot
			}
		case plan.GroupExpr:
			key.Slot = e.Slot
		}
		if key.Slot >= 0 {
			continue
		}
		// SQL99 resolves against the accumulated ORDER target list, including
		// preceding hidden targets. Direction and NULL policy remain per key.
		for previous := range i {
			if keys[previous].Cache >= 0 && ((key.TargetIdentity != 0 && keys[previous].TargetIdentity == key.TargetIdentity) || (key.TargetIdentity == 0 && b.sameGroupExpression(keys[previous].Expression, key.Expression))) {
				key.Cache = keys[previous].Cache
				break
			}
		}
		if key.Cache < 0 {
			key.Cache = computed
			computed++
		}
	}
	for j, output := range projection {
		chosen, demanded := -1, false
		for k, key := range keys {
			if key.OutputOrdinal == j+1 {
				demanded = true
				if key.Cache >= 0 {
					chosen = k
					break
				}
			}
		}
		if !demanded {
			for k, key := range keys {
				if key.Cache >= 0 && b.sameGroupExpression(output, key.Expression) {
					chosen = k
					break
				}
			}
		}
		if chosen >= 0 {
			key := &keys[chosen]
			key.Output = true
			properties := output.Properties()
			properties.StageDependent = true
			info := expr.InfoOf(output)
			info.Property = properties
			projection[j] = plan.CachedOrderExpr{Info: info, Slot: key.Cache}
		}
	}
	return &plan.SortNode{QuerySchema: root.Schema(), Child: root, Keys: keys, CacheWidth: computed}, nil
}

func (c *EvalContext) EvalCachedOrder(e plan.CachedOrderExpr) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Slot < 0 || e.Slot >= len(c.orderValues) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing ordering output")
	}
	return c.orderValues[e.Slot], nil
}
