package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type RegressionAggregate struct {
	input     *sqltype.Descriptor
	usage     sqltype.Usage
	operation sqltype.RegressionOperation
}

func NewRegressionAggregate(input *sqltype.Descriptor, usage sqltype.Usage, operation sqltype.RegressionOperation) RegressionAggregate {
	return RegressionAggregate{input, usage, operation}
}
func (p RegressionAggregate) InputType() *sqltype.Descriptor { return p.input }
func (p RegressionAggregate) ResultUsage() sqltype.Usage     { return p.usage }
func (p RegressionAggregate) Reconstruct(input *sqltype.Descriptor, usage sqltype.Usage) RegressionAggregate {
	return NewRegressionAggregate(input, usage, p.operation)
}
func (p RegressionAggregate) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.FloatRegressionState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p RegressionAggregate) Advance(ctx context.Context, state any, y, x sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.FloatRegressionState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid regression state")
	}
	if y.Type() == nil || x.Type() == nil || y.Type() != p.input || x.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid regression datum")
	}
	if err := s.Add(y, x); err != nil {
		return err
	}
	return ctx.Err()
}
func (p RegressionAggregate) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.FloatRegressionState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid regression state")
	}
	value, err := s.Result(p.usage.Type, p.operation)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
