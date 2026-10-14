package functions

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

type CountProvider struct{ usage sqltype.Usage }
type countState struct{ count int64 }

func (p CountProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p CountProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &countState{}, nil
}
func (p CountProvider) Advance(ctx context.Context, state any, _, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*countState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid count state")
	}
	if s.count == math.MaxInt64 {
		return sqlError("22003", "bigint out of range")
	}
	s.count++
	return ctx.Err()
}
func (p CountProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*countState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid count state")
	}
	return sqltype.Integer(p.usage.Type, s.count)
}

func NewCountProvider(usage sqltype.Usage) CountProvider { return CountProvider{usage} }
