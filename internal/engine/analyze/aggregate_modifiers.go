package analyze

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
func (b *Binder) BindAggregateFilter(raw parser.AggregateExpr) (expr.Expr, error) {
	if raw.Filter == nil {
		return nil, nil
	}
	clause, regexpClause := b.Clause.AggregateClause, b.Clause.RegexpClause
	b.Clause.AggregateClause, b.Clause.RegexpClause = "FILTER", "FILTER"
	defer func() { b.Clause.AggregateClause, b.Clause.RegexpClause = clause, regexpClause }()
	return b.BindBoolean(raw.Filter, ProjectionContext, "FILTER")
}

// SQL99 ignores implicit coercion of an existing target, not explicit casts.
func (b *Binder) sameAggregateTarget(target, expression expr.Expr) bool {
	for {
		c, ok := target.(plan.CoercionExpr)
		if !ok || c.Mode != sqltype.Implicit {
			break
		}
		target = c.Child
	}
	return b.SameGroupExpression(target, expression)
}

func (b *Binder) finishAggregate(raw parser.AggregateExpr, call plan.AggregateCall, filter expr.Expr, info expr.Info) (expr.Expr, error) {
	call.Filter = filter
	objectProvider, object := call.Provider.(functions.JSONObjectAggregate)
	if raw.Distinct && !object {
		for i, argument := range call.Arguments {
			var err error
			call.Arguments[i], err = b.FinalizeProjection(argument)
			if err != nil {
				return nil, err
			}
		}
	}
	if _, ordered := call.Provider.(functions.OrderedAggregate); !ordered {
		call.Order = make([]plan.SortKey, len(raw.Order))
	}
	if len(raw.Order) > 0 && call.StageWidth == 0 {
		call.StageWidth = len(call.Arguments)
	}
	for i, key := range raw.Order {
		if _, ordered := call.Provider.(functions.OrderedAggregate); ordered {
			break
		}
		expression, err := b.Bind(key.Expr, ExpressionPolicy{Context: ProjectionContext})
		if err != nil {
			return nil, err
		}
		slot := -1
		if object {
			// Match before UNKNOWN coercion changes structural usage. ORDER
			// selects one first equal input, not every use of its parameter.
			for j, argument := range call.Arguments {
				if b.sameAggregateTarget(argument, expression) {
					slot = j
					call.Arguments[j], err = b.completeJSONObjectAggregateInput(argument)
					break
				}
			}
			if err != nil {
				return nil, err
			}
		} else {
			// Preserve other providers' existing provisional-target inference.
			for j, argument := range call.Arguments {
				if argument.ResultUsage().Type == nil && b.sameAggregateTarget(argument, expression) {
					call.Arguments[j], err = b.FinalizeProjection(argument)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		expression, err = b.FinalizeProjection(expression)
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
			for j, argument := range call.Arguments {
				if b.sameAggregateTarget(argument, expression) {
					slot = j
					break
				}
			}
		}
		if slot >= 0 {
			comparison, err = functions.AggregateOrdering(call.Arguments[slot].ResultUsage(), key.Pos, false)
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
					slot = j + len(call.Arguments)
					break
				}
			}
		}
		cache := slot
		if slot < 0 {
			cache = call.StageWidth
			call.StageWidth++
		} else if slot >= len(call.Arguments) {
			cache = call.Order[slot-len(call.Arguments)].Cache
		}
		call.Order[i] = plan.SortKey{Expression: expression, Comparison: comparison, Position: key.Pos.Character, Desc: key.Desc, NullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, Slot: slot, Cache: cache}
	}
	if raw.Distinct {
		if object {
			for i, argument := range call.Arguments {
				var err error
				call.Arguments[i], err = b.completeJSONObjectAggregateInput(argument)
				if err != nil {
					return nil, err
				}
			}
		}
		call.Distinct = make([]plan.Grouping, len(raw.Args))
		for i, arg := range call.Arguments {
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
	if err := b.Grouping.ArgumentOwner.nestingError(); err != nil {
		return nil, err
	}
	if object {
		provider, err := objectProvider.Complete(call.Arguments[0].ResultUsage(), call.Arguments[1].ResultUsage())
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
	slot := len(b.Grouping.Aggregates)
	if properties.Volatility == expr.Immutable {
		for i, existing := range b.Grouping.Aggregates {
			if b.SameAggregateCall(call, existing) {
				slot = i
				break
			}
		}
	}
	if slot == len(b.Grouping.Aggregates) {
		b.Grouping.Aggregates = append(b.Grouping.Aggregates, call)
	}
	return plan.AggregateExpr{Info: info, Slot: slot, Frame: b.FrameID()}, nil
}


func (b *Binder) SameAggregateCall(a, other plan.AggregateCall) bool {
	if a.RoutineRecipe != other.RoutineRecipe {
		return false
	}
	builtin := a.Procedure != nil && a.Procedure.Builtin != nil
	otherBuiltin := other.Procedure != nil && other.Procedure.Builtin != nil
	if builtin != otherBuiltin || !builtin && !plan.SameIntaggIdentity(a.Procedure, a.Ref, other.Procedure, other.Ref) {
		return false
	}
	if builtin && (a.Routine != other.Routine || a.Procedure != other.Procedure) {
		return false
	}
	if p, ok := a.Provider.(functions.JSONObjectAggregate); ok {
		q, same := other.Provider.(functions.JSONObjectAggregate)
		if !same || p.Unique() != q.Unique() {
			return false
		}
	}
	if !builtin && a.Procedure != nil && a.Routine != other.Routine || a.Operation != other.Operation || a.IncludeNulls != other.IncludeNulls || a.StageWidth != other.StageWidth || a.Provider.ResultUsage() != other.Provider.ResultUsage() || len(a.Distinct) != len(other.Distinct) || len(a.Order) != len(other.Order) || len(a.Direct) != len(other.Direct) || len(a.Arguments) != len(other.Arguments) || !b.SameGroupExpression(a.Filter, other.Filter) {
		return false
	}
	for i, argument := range a.Arguments {
		if !b.SameGroupExpression(argument, other.Arguments[i]) {
			return false
		}
	}
	for i, direct := range a.Direct {
		if !b.SameGroupExpression(direct, other.Direct[i]) {
			return false
		}
	}
	for i, key := range a.Order {
		o := other.Order[i]
		if key.Desc != o.Desc || key.NullsFirst != o.NullsFirst || key.Slot != o.Slot || key.Cache != o.Cache || !b.SameGroupExpression(key.Expression, o.Expression) {
			return false
		}
	}
	return true
}

// A modifier resolves one particular ANY input occurrence, not every use of its
// parameter number. Keep earlier occurrences for finish's 42P08 validation.
func (b *Binder) completeJSONObjectAggregateInput(node expr.Expr) (expr.Expr, error) {
	p, unknown := node.(plan.ParameterExpr)
	if !unknown || p.Usage.Type != nil {
		return node, nil
	}
	resolved, err := b.FinalizeProjection(node)
	if err != nil {
		return nil, err
	}
	for i, prior := range b.Params.AnyParameters {
		if prior.Number != p.Number || prior.Pos != p.Pos {
			continue
		}
		copy(b.Params.AnyParameters[i:], b.Params.AnyParameters[i+1:])
		last := len(b.Params.AnyParameters) - 1
		b.Params.AnyParameters[last] = plan.ParameterExpr{}
		b.Params.AnyParameters = b.Params.AnyParameters[:last]
		break
	}
	return resolved, nil
}
