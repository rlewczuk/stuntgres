package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype"
)

type moneySumProvider struct {
	usage sqltype.Usage
	bound sqltype.Arithmetic
}

type moneySumState struct {
	sum  sqltype.Value
	seen bool
}

func (p moneySumProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p moneySumProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.usage.Type == nil || p.usage.Type.ID() != sqltype.Money || p.bound.ResultType() != p.usage.Type {
		return nil, fmt.Errorf("stuntgres: invalid money sum descriptor")
	}
	state := &moneySumState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p moneySumProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*moneySumState)
	if !ok || s == nil || value.Type() == nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid money sum state or datum")
	}
	if !value.IsNull() {
		next := value
		if s.seen {
			var err error
			next, err = p.bound.Binary(ctx, s.sum, value, sqltype.InputEnvironment{})
			if err != nil {
				return err
			}
		}
		s.sum, s.seen = next, true
	}
	return ctx.Err()
}
func (p moneySumProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*moneySumState)
	if !ok || s == nil || p.usage.Type == nil || p.usage.Type.ID() != sqltype.Money || (s.seen && s.sum.Type() != p.usage.Type) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid money sum state")
	}
	value := sqltype.Null(p.usage.Type)
	if s.seen {
		value = s.sum
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
