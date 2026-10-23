package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type JSONArrayAggregateProvider struct {
	input, usage sqltype.Usage
	absent       bool
	plan         sqltype.JSONArrayAggregatePlan
}

func (p JSONArrayAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p JSONArrayAggregateProvider) Init(ctx context.Context) (any, error) {
	return p.InitWithEnvironment(ctx, sqltype.OutputEnvironment{})
}
func (p JSONArrayAggregateProvider) InitWithEnvironment(ctx context.Context, env sqltype.OutputEnvironment) (any, error) {
	return p.plan.Init(ctx, env)
}
func (p JSONArrayAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	v := values[0]
	s, ok := state.(*sqltype.JSONArrayAggregateState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid JSON aggregate state")
	}
	return p.plan.Advance(ctx, s, v)
}
func (p JSONArrayAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	s, ok := state.(*sqltype.JSONArrayAggregateState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid JSON aggregate state")
	}
	return p.plan.Finalize(ctx, s)
}
func NewJSONArrayAggregateProvider(input, usage sqltype.Usage, absent bool, plan sqltype.JSONArrayAggregatePlan) JSONArrayAggregateProvider {
	return JSONArrayAggregateProvider{input, usage, absent, plan}
}
func (p JSONArrayAggregateProvider) InputUsage() sqltype.Usage { return p.input }
func (p JSONArrayAggregateProvider) Absent() bool              { return p.absent }
