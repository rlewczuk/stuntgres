package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type RangeAggregateProvider struct {
	input, usage sqltype.Usage
	operation    sqltype.RangeAggregateOperation
	bound        sqltype.RangeAggregate
}

func (p RangeAggregateProvider) ResultUsage() sqltype.Usage            { return p.usage }
func (p RangeAggregateProvider) Init(ctx context.Context) (any, error) { return p.bound.Init(ctx) }
func (p RangeAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, evaluation expr.Context) error {
	value := values[0]
	s, _ := state.(*sqltype.RangeAggregateState)
	var input sqltype.InputEnvironment
	if evaluation != nil {
		input = evaluation.FunctionInput()
	}
	return p.bound.AdvanceWithEnvironment(ctx, s, value, input)
}
func (p RangeAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.Finalize(ctx, s)
}

func (p RangeAggregateProvider) InitWithContext(ctx context.Context, _ expr.Context) (any, error) {
	return p.bound.Init(ctx)
}

func (p RangeAggregateProvider) FinalizeWithContext(ctx context.Context, state any, _ []sqltype.Value, evaluation expr.Context) (sqltype.Value, error) {
	s, _ := state.(*sqltype.RangeAggregateState)
	return p.bound.FinalizeWithEnvironment(ctx, s, evaluation.FunctionInput())
}

func NewRangeAggregateProvider(input, usage sqltype.Usage, operation sqltype.RangeAggregateOperation, bound sqltype.RangeAggregate) RangeAggregateProvider {
	return RangeAggregateProvider{input, usage, operation, bound}
}

func (p RangeAggregateProvider) VisitIndexSupports(visit func(sqltype.IndexSupport, uint16) error) error {
	return p.bound.VisitIndexSupports(visit)
}
func (p RangeAggregateProvider) InputUsage() sqltype.Usage                  { return p.input }
func (p RangeAggregateProvider) Operation() sqltype.RangeAggregateOperation { return p.operation }
