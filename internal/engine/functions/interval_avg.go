package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type IntervalAvg struct{ usage sqltype.Usage }

func NewIntervalAvg(usage sqltype.Usage) IntervalAvg              { return IntervalAvg{usage} }
func (p IntervalAvg) Reconstruct(usage sqltype.Usage) IntervalAvg { return IntervalAvg{usage} }

func (p IntervalAvg) ResultUsage() sqltype.Usage { return p.usage }
func (p IntervalAvg) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := &sqltype.IntervalAvgState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p IntervalAvg) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
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
func (p IntervalAvg) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
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
