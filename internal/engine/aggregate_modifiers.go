package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// FILTER is transformed after ordinary children, but before signature lookup.
func (b *expressionBinder) bindAggregateFilter(raw parser.AggregateExpr) (BoundExpr, error) {
	if raw.Filter == nil {
		return nil, nil
	}
	clause := b.aggregateClause
	b.aggregateClause = "FILTER"
	defer func() { b.aggregateClause = clause }()
	return b.bindBoolean(raw.Filter, projectionContext, "FILTER")
}

func aggregateOrdering(usage sqltype.Usage, pos parser.Position, distinct bool) (sqltype.Comparison, error) {
	base := usage.Type.BaseType()
	if base.IsArray() || base.ID() == sqltype.PgNodeTree ||
		base.ID() == sqltype.Int4Range || base.ID() == sqltype.Int8Range || base.ID() == sqltype.NumRange ||
		base.ID() == sqltype.Int4Multirange || base.ID() == sqltype.Int8Multirange || base.ID() == sqltype.NumMultirange {
		kind := "ordering"
		if distinct {
			kind = "DISTINCT"
		}
		return sqltype.Comparison{}, expressionError("0A000", "aggregate "+kind+" is unsupported for type "+base.DiagnosticName(), pos)
	}
	comparison, err := sqltype.BindOrdering(usage)
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Position = fmt.Sprint(pos.Character)
		if distinct {
			copy.Hint = ""
			copy.Detail = "Aggregates with DISTINCT must be able to sort their inputs."
		}
		return sqltype.Comparison{}, &copy
	}
	return comparison, err
}

// SQL99 ignores implicit coercion of an existing target, not explicit casts.
func (b *expressionBinder) sameAggregateTarget(target, expression BoundExpr) bool {
	for {
		c, ok := target.(coercionExpression)
		if !ok || c.mode != sqltype.Implicit {
			break
		}
		target = c.child
	}
	return b.sameGroupExpression(target, expression)
}

func (b *expressionBinder) finishAggregate(raw parser.AggregateExpr, call aggregateCall, filter BoundExpr, info expressionInfo) (BoundExpr, error) {
	call.filter = filter
	if raw.Distinct {
		var err error
		call.argument, err = b.finalizeProjection(call.argument)
		if err != nil {
			return nil, err
		}
	}
	call.order = make([]sortKey, len(raw.Order))
	if len(raw.Order) > 0 {
		call.stageWidth = 2
	}
	for i, key := range raw.Order {
		expression, err := b.bind(key.Expr, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		// SQL99 matches the provisional target before resolving an UNKNOWN key.
		// In particular, count($1 ORDER BY $1) resolves the shared input to TEXT.
		if call.argument != nil && call.argument.ResultUsage().Type == nil && b.sameAggregateTarget(call.argument, expression) {
			call.argument, err = b.finalizeProjection(call.argument)
			if err != nil {
				return nil, err
			}
		}
		expression, err = b.finalizeProjection(expression)
		if err != nil {
			return nil, err
		}
		if expression.CollationIndeterminate() {
			left, right, position := expression.ImplicitCollationConflict()
			d := expressionError("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
			d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
			return nil, d
		}
		// Positions in the order list always belong to the original occurrence.
		comparison, err := aggregateOrdering(expression.ResultUsage(), key.Pos, false)
		if err != nil {
			return nil, err
		}
		slot := -1
		if call.argument != nil && b.sameAggregateTarget(call.argument, expression) {
			slot = 0
		} else if call.separator != nil && b.sameAggregateTarget(call.separator, expression) {
			slot = 1
		}
		if slot == 0 {
			comparison, err = aggregateOrdering(call.argument.ResultUsage(), key.Pos, false)
		} else if slot == 1 {
			comparison, err = aggregateOrdering(call.separator.ResultUsage(), key.Pos, false)
		}
		if err != nil {
			return nil, err
		}
		if raw.Distinct && slot < 0 {
			return nil, expressionError("42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", parser.ExpressionStart(key.Expr))
		}
		if slot < 0 {
			for j, prior := range call.order[:i] {
				if b.sameAggregateTarget(prior.expression, expression) {
					slot = j + 2
					break
				}
			}
		}
		cache := slot
		if slot < 0 {
			cache = call.stageWidth
			call.stageWidth++
		} else if slot >= 2 {
			cache = call.order[slot-2].cache
		}
		call.order[i] = sortKey{expression: expression, comparison: comparison, position: key.Pos.Character, desc: key.Desc, nullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, slot: slot, cache: cache}
	}
	if raw.Distinct {
		call.distinct = make([]sqltype.Grouping, len(raw.Args))
		args := [2]BoundExpr{call.argument, call.separator}
		for i, arg := range args {
			if arg == nil {
				continue
			}
			pos := parser.ExpressionStart(raw.Args[i])
			grouping, err := b.bindDistinctGrouping(arg)
			if d, ok := err.(*sqlstate.Error); ok && d.Code != "42P21" {
				copy := *d
				copy.Position = fmt.Sprint(pos.Character)
				return nil, &copy
			}
			if err != nil {
				return nil, err
			}
			if _, err := aggregateOrdering(arg.ResultUsage(), pos, true); err != nil {
				return nil, err
			}
			call.distinct[i] = grouping
		}
	}
	if err := b.argumentOwner.nestingError(); err != nil {
		return nil, err
	}
	properties := ExprProperties{StageDependent: true}
	call.expressions(func(expr BoundExpr) {
		if expr != nil {
			properties = mergeExpressionProperties(properties, expr.Properties())
		}
	})
	info.properties = properties
	slot := len(b.aggregates)
	if properties.Volatility == Immutable {
		for i, existing := range b.aggregates {
			if b.sameAggregateCall(call, existing) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.aggregates) {
		b.aggregates = append(b.aggregates, call)
	}
	return aggregateExpression{expressionInfo: info, slot: slot, frame: b.frameID()}, nil
}

func (b *expressionBinder) sameAggregateCall(a, other aggregateCall) bool {
	if a.operation != other.operation || a.includeNulls != other.includeNulls || a.provider.ResultUsage() != other.provider.ResultUsage() || len(a.distinct) != len(other.distinct) || len(a.order) != len(other.order) || !b.sameGroupExpression(a.argument, other.argument) || !b.sameGroupExpression(a.separator, other.separator) || !b.sameGroupExpression(a.filter, other.filter) {
		return false
	}
	for i, key := range a.order {
		o := other.order[i]
		if key.desc != o.desc || key.nullsFirst != o.nullsFirst || key.slot != o.slot || !b.sameGroupExpression(key.expression, o.expression) {
			return false
		}
	}
	return true
}
