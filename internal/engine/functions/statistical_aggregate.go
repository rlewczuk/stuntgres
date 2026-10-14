package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// StatisticalAggregate is immutable; each group's Init owns its arithmetic.
type StatisticalAggregate struct {
	input          *sqltype.Descriptor
	usage          sqltype.Usage
	sample, stddev bool
}

func NewStatisticalAggregate(input *sqltype.Descriptor, usage sqltype.Usage, sample, stddev bool) StatisticalAggregate {
	return StatisticalAggregate{input, usage, sample, stddev}
}
func (p StatisticalAggregate) InputType() *sqltype.Descriptor { return p.input }
func (p StatisticalAggregate) ResultUsage() sqltype.Usage     { return p.usage }
func (p StatisticalAggregate) Reconstruct(input *sqltype.Descriptor, usage sqltype.Usage) StatisticalAggregate {
	return NewStatisticalAggregate(input, usage, p.sample, p.stddev)
}
func (p StatisticalAggregate) floating() bool {
	return p.input.ID() == sqltype.Float4 || p.input.ID() == sqltype.Float8
}
func (p StatisticalAggregate) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var state any
	if p.floating() {
		state = &sqltype.FloatAvgState{}
	} else {
		state = &sqltype.NumericMomentsState{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p StatisticalAggregate) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid statistical aggregate datum")
	}
	if p.floating() {
		s, ok := state.(*sqltype.FloatAvgState)
		if !ok || s == nil {
			return fmt.Errorf("stuntgres: invalid float moments state")
		}
		if err := s.Add(value); err != nil {
			return err
		}
	} else {
		s, ok := state.(*sqltype.NumericMomentsState)
		if !ok || s == nil {
			return fmt.Errorf("stuntgres: invalid numeric moments state")
		}
		if err := s.Add(ctx, value); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (p StatisticalAggregate) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var value sqltype.Value
	var err error
	if p.floating() {
		s, ok := state.(*sqltype.FloatAvgState)
		if !ok || s == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid float moments state")
		}
		value, err = s.MomentsResult(p.usage.Type, p.sample, p.stddev)
	} else {
		s, ok := state.(*sqltype.NumericMomentsState)
		if !ok || s == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid numeric moments state")
		}
		value, err = s.Result(ctx, p.usage.Type, p.sample, p.stddev)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
