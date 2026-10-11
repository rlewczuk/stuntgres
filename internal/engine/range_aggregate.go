package engine

import (
	"context"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type rangeAggregateProvider struct {
	input, usage sqltype.Usage
	operation    sqltype.RangeAggregateOperation
	bound        sqltype.RangeAggregate
}

func (p rangeAggregateProvider) ResultUsage() sqltype.Usage            { return p.usage }
func (p rangeAggregateProvider) Init(ctx context.Context) (any, error) { return p.bound.Init(ctx) }
func (p rangeAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.Advance(ctx, s, value)
}
func (p rangeAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.Finalize(ctx, s)
}

func (b *expressionBinder) bindRangeAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	for i, child := range raw.Args {
		bound, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i], err = b.extremaArgumentTypeName(bound)
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := b.resolveRangeAggregateSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	input := arg.ResultUsage().Type
	operation, identity := sqltype.RangeAggregateUnion, aggregateRangeUnion
	if raw.Name == "range_intersect_agg" {
		operation, identity = sqltype.RangeAggregateIntersection, aggregateRangeIntersection
	}
	bound, err := sqltype.BindRangeAggregate(input, operation)
	if err != nil {
		return nil, err
	}
	usage := bound.ResultUsage()
	provider := rangeAggregateProvider{input: input.Usage(), usage: usage, operation: operation, bound: bound}
	call := aggregateCall{provider: provider, argument: arg, operation: identity}
	properties := arg.Properties()
	info := b.info(usage, raw.Pos, ExprProperties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveRangeAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, error) {
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	input := operand.descriptor()
	if input == nil {
		d := expressionError("42725", "function "+objectNameText(name)+"(unknown) is not unique", raw.Pos).(*sqlstate.Error)
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
		return nil, d
	}
	input = input.BaseType()
	switch input.ID() {
	case sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
	default:
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	return b.coerceConditional(operand, input.Usage(), raw.Name)
}
