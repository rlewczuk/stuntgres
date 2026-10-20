package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// FILTER is transformed after ordinary children, but before signature lookup.
func (b *expressionBinder) bindAggregateFilter(raw parser.AggregateExpr) (expr.Expr, error) {
	if raw.Filter == nil {
		return nil, nil
	}
	clause, regexpClause := b.clause.aggregateClause, b.clause.regexpClause
	b.clause.aggregateClause, b.clause.regexpClause = "FILTER", "FILTER"
	defer func() { b.clause.aggregateClause, b.clause.regexpClause = clause, regexpClause }()
	return b.bindBoolean(raw.Filter, projectionContext, "FILTER")
}

// SQL99 ignores implicit coercion of an existing target, not explicit casts.
func (b *expressionBinder) sameAggregateTarget(target, expression expr.Expr) bool {
	for {
		c, ok := target.(plan.CoercionExpr)
		if !ok || c.Mode != sqltype.Implicit {
			break
		}
		target = c.Child
	}
	return b.sameGroupExpression(target, expression)
}

func (b *expressionBinder) finishAggregate(raw parser.AggregateExpr, call plan.AggregateCall, filter expr.Expr, info expr.Info) (expr.Expr, error) {
	call.Filter = filter
	objectProvider, object := call.Provider.(functions.JSONObjectAggregate)
	if raw.Distinct && !object {
		var err error
		call.Argument, err = b.finalizeProjection(call.Argument)
		if err != nil {
			return nil, err
		}
	}
	if _, ordered := call.Provider.(functions.OrderedAggregate); !ordered {
		call.Order = make([]plan.SortKey, len(raw.Order))
	}
	if len(raw.Order) > 0 && call.StageWidth == 0 {
		call.StageWidth = 2
	}
	for i, key := range raw.Order {
		if _, ordered := call.Provider.(functions.OrderedAggregate); ordered {
			break
		}
		expression, err := b.bind(key.Expr, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		slot := -1
		if object {
			// Match before UNKNOWN coercion changes structural usage. ORDER
			// selects one first equal input, not every use of its parameter.
			if b.sameAggregateTarget(call.Argument, expression) {
				slot = 0
				call.Argument, err = b.completeJSONObjectAggregateInput(call.Argument)
			} else if b.sameAggregateTarget(call.Separator, expression) {
				slot = 1
				call.Separator, err = b.completeJSONObjectAggregateInput(call.Separator)
			}
			if err != nil {
				return nil, err
			}
		} else {
			// Preserve other providers' existing provisional-target inference.
			if call.Argument != nil && call.Argument.ResultUsage().Type == nil && b.sameAggregateTarget(call.Argument, expression) {
				call.Argument, err = b.finalizeProjection(call.Argument)
				if err != nil {
					return nil, err
				}
			}
		}
		expression, err = b.finalizeProjection(expression)
		if err != nil {
			return nil, err
		}
		if expression.CollationIndeterminate() {
			left, right, position := expression.ImplicitCollationConflict()
			d := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
			d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
			return nil, d
		}
		// Positions in the order list always belong to the original occurrence.
		comparison, err := functions.AggregateOrdering(expression.ResultUsage(), key.Pos, false)
		if err != nil {
			return nil, err
		}
		if !object {
			if call.Argument != nil && b.sameAggregateTarget(call.Argument, expression) {
				slot = 0
			} else if call.Separator != nil && b.sameAggregateTarget(call.Separator, expression) {
				slot = 1
			}
		}
		if slot == 0 {
			comparison, err = functions.AggregateOrdering(call.Argument.ResultUsage(), key.Pos, false)
		} else if slot == 1 {
			comparison, err = functions.AggregateOrdering(call.Separator.ResultUsage(), key.Pos, false)
		}
		if err != nil {
			return nil, err
		}
		if raw.Distinct && slot < 0 {
			return nil, expr.ErrorAt("42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", parser.ExpressionStart(key.Expr))
		}
		if slot < 0 {
			for j, prior := range call.Order[:i] {
				if b.sameAggregateTarget(prior.Expression, expression) {
					slot = j + 2
					break
				}
			}
		}
		cache := slot
		if slot < 0 {
			cache = call.StageWidth
			call.StageWidth++
		} else if slot >= 2 {
			cache = call.Order[slot-2].Cache
		}
		call.Order[i] = plan.SortKey{Expression: expression, Comparison: comparison, Position: key.Pos.Character, Desc: key.Desc, NullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, Slot: slot, Cache: cache}
	}
	if raw.Distinct {
		if object {
			var err error
			call.Argument, err = b.completeJSONObjectAggregateInput(call.Argument)
			if err != nil {
				return nil, err
			}
			call.Separator, err = b.completeJSONObjectAggregateInput(call.Separator)
			if err != nil {
				return nil, err
			}
		}
		call.Distinct = make([]plan.Grouping, len(raw.Args))
		args := [2]expr.Expr{call.Argument, call.Separator}
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
			if _, err := functions.AggregateOrdering(arg.ResultUsage(), pos, true); err != nil {
				return nil, err
			}
			call.Distinct[i] = grouping
		}
	}
	if err := b.grouping.argumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if object {
		provider, err := objectProvider.Complete(call.Argument.ResultUsage(), call.Separator.ResultUsage())
		if err != nil {
			return nil, err
		}
		call.Provider = provider
	}
	properties := expr.Properties{StageDependent: true}
	call.Expressions(func(bound expr.Expr) {
		if bound != nil {
			properties = expr.MergeProperties(properties, bound.Properties())
		}
	})
	info.Property = properties
	slot := len(b.grouping.aggregates)
	if properties.Volatility == expr.Immutable {
		for i, existing := range b.grouping.aggregates {
			if b.sameAggregateCall(call, existing) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.grouping.aggregates) {
		b.grouping.aggregates = append(b.grouping.aggregates, call)
	}
	return plan.AggregateExpr{Info: info, Slot: slot, Frame: b.frameID()}, nil
}

func (b *expressionBinder) sameAggregateCall(a, other plan.AggregateCall) bool {
	if !plan.SameIntaggIdentity(a.Procedure, a.Ref, other.Procedure, other.Ref) {
		return false
	}
	if p, ok := a.Provider.(functions.JSONObjectAggregate); ok {
		q, same := other.Provider.(functions.JSONObjectAggregate)
		if !same || p.Unique() != q.Unique() {
			return false
		}
	}
	if a.Operation != other.Operation || a.IncludeNulls != other.IncludeNulls || a.StageWidth != other.StageWidth || a.Provider.ResultUsage() != other.Provider.ResultUsage() || len(a.Distinct) != len(other.Distinct) || len(a.Order) != len(other.Order) || len(a.Direct) != len(other.Direct) || !b.sameGroupExpression(a.Argument, other.Argument) || !b.sameGroupExpression(a.Separator, other.Separator) || !b.sameGroupExpression(a.Filter, other.Filter) {
		return false
	}
	for i, direct := range a.Direct {
		if !b.sameGroupExpression(direct, other.Direct[i]) {
			return false
		}
	}
	for i, key := range a.Order {
		o := other.Order[i]
		if key.Desc != o.Desc || key.NullsFirst != o.NullsFirst || key.Slot != o.Slot || key.Cache != o.Cache || !b.sameGroupExpression(key.Expression, o.Expression) {
			return false
		}
	}
	return true
}

// A modifier resolves one particular ANY input occurrence, not every use of its
// parameter number. Keep earlier occurrences for finish's 42P08 validation.
func (b *expressionBinder) completeJSONObjectAggregateInput(node expr.Expr) (expr.Expr, error) {
	p, unknown := node.(plan.ParameterExpr)
	if !unknown || p.Usage.Type != nil {
		return node, nil
	}
	resolved, err := b.finalizeProjection(node)
	if err != nil {
		return nil, err
	}
	for i, prior := range b.params.anyParameters {
		if prior.Number != p.Number || prior.Pos != p.Pos {
			continue
		}
		copy(b.params.anyParameters[i:], b.params.anyParameters[i+1:])
		last := len(b.params.anyParameters) - 1
		b.params.anyParameters[last] = plan.ParameterExpr{}
		b.params.anyParameters = b.params.anyParameters[:last]
		break
	}
	return resolved, nil
}
