package functions

import (
	"context"
	"stuntgres/internal/sqltype"
)

type RangeAggregateProvider struct {
	input, usage sqltype.Usage
	operation    sqltype.RangeAggregateOperation
	bound        sqltype.RangeAggregate
}

func (p RangeAggregateProvider) ResultUsage() sqltype.Usage            { return p.usage }
func (p RangeAggregateProvider) Init(ctx context.Context) (any, error) { return p.bound.Init(ctx) }
func (p RangeAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.Advance(ctx, s, value)
}
func (p RangeAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.Finalize(ctx, s)
}

func NewRangeAggregateProvider(input, usage sqltype.Usage, operation sqltype.RangeAggregateOperation, bound sqltype.RangeAggregate) RangeAggregateProvider {
	return RangeAggregateProvider{input, usage, operation, bound}
}
func (p RangeAggregateProvider) InputUsage() sqltype.Usage                  { return p.input }
func (p RangeAggregateProvider) Operation() sqltype.RangeAggregateOperation { return p.operation }
