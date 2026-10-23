package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type NumericAvg struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}

func NewNumericAvg(input *sqltype.Descriptor, usage sqltype.Usage) NumericAvg {
	return NumericAvg{input, usage}
}
func (p NumericAvg) InputType() *sqltype.Descriptor { return p.input }
func (p NumericAvg) Reconstruct(input *sqltype.Descriptor, usage sqltype.Usage) NumericAvg {
	return NumericAvg{input, usage}
}

func (p NumericAvg) ResultUsage() sqltype.Usage { return p.usage }
func (p NumericAvg) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.NumericAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p NumericAvg) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
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
func (p NumericAvg) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
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
