package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/sqltype"
)

// MoneySum is sum(money): money addition with overflow errors, NULL for no
// input rows.
type MoneySum struct {
	usage sqltype.Usage
	bound sqltype.Arithmetic
}

type moneySumState struct {
	sum  sqltype.Value
	seen bool
}

func (p MoneySum) ResultUsage() sqltype.Usage { return p.usage }
func (p MoneySum) Init(ctx context.Context) (any, error) {
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
func (p MoneySum) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
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
func (p MoneySum) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
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

// NewMoneySum builds sum(money) over the bound money addition.
func NewMoneySum(usage sqltype.Usage, bound sqltype.Arithmetic) MoneySum {
	return MoneySum{usage: usage, bound: bound}
}

// Reconstruct rebinds the provider for a remapped money result usage.
func (p MoneySum) Reconstruct(usage sqltype.Usage) (MoneySum, error) {
	bound, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, usage.Type, usage.Type, usage.Type)
	return MoneySum{usage: usage, bound: bound}, err
}

// IntervalSum is sum(interval), accumulated exactly and normalized at
// finalization.
type IntervalSum struct{ usage sqltype.Usage }

func (p IntervalSum) ResultUsage() sqltype.Usage { return p.usage }
func (p IntervalSum) Init(ctx context.Context) (any, error) {
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
func (p IntervalSum) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
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
func (p IntervalSum) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
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

// NewIntervalSum builds sum(interval).
func NewIntervalSum(usage sqltype.Usage) IntervalSum { return IntervalSum{usage} }

// Reconstruct returns the provider for a remapped interval result usage.
func (p IntervalSum) Reconstruct(usage sqltype.Usage) IntervalSum { return IntervalSum{usage} }
