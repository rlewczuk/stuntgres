package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type numericAvgProvider struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}

func (p numericAvgProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p numericAvgProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.NumericAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p numericAvgProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.NumericAvgState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid numeric average state")
	}
	if value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid numeric average datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p numericAvgProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.NumericAvgState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid numeric average state")
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
