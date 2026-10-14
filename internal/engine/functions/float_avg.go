package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type FloatAvg struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}

func NewFloatAvg(input *sqltype.Descriptor, usage sqltype.Usage) FloatAvg {
	return FloatAvg{input, usage}
}
func (p FloatAvg) InputType() *sqltype.Descriptor { return p.input }
func (p FloatAvg) Reconstruct(input *sqltype.Descriptor, usage sqltype.Usage) FloatAvg {
	return FloatAvg{input, usage}
}

func (p FloatAvg) ResultUsage() sqltype.Usage { return p.usage }
func (p FloatAvg) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.FloatAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p FloatAvg) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.FloatAvgState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid float average state")
	}
	if value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid float average datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p FloatAvg) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.FloatAvgState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid float average state")
	}
	value, err := s.Result(p.usage.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
