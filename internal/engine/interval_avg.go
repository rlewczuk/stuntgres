package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type intervalAvgProvider struct{ usage sqltype.Usage }

func (p intervalAvgProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p intervalAvgProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.IntervalAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p intervalAvgProvider) Advance(ctx context.Context, state any, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid interval average state")
	}
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid interval average datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p intervalAvgProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.IntervalAvgState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid interval average state")
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
