package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

type intervalSumProvider struct{ usage sqltype.Usage }

func (p intervalSumProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p intervalSumProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.usage.Type == nil || p.usage.Type.ID() != sqltype.Interval {
		return nil, fmt.Errorf("stuntgres: invalid interval sum descriptor")
	}
	state := &sqltype.IntervalAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p intervalSumProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil || value.Type() == nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid interval sum state or datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p intervalSumProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid interval sum state")
	}
	value, err := s.SumResult(p.usage.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
