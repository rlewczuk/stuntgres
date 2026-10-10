package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

type integralSumProvider struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}
type integralSumState struct {
	sum  int64
	seen bool
}

func (p integralSumProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p integralSumProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.input == nil || (p.input.ID() != sqltype.Int2 && p.input.ID() != sqltype.Int4) || p.usage.Type == nil || p.usage.Type.ID() != sqltype.Int8 {
		return nil, fmt.Errorf("stuntgres: invalid integral sum descriptor")
	}
	state := &integralSumState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p integralSumProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*integralSumState)
	if !ok || s == nil || value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid integral sum state or datum")
	}
	if !value.IsNull() {
		n, err := value.Integer()
		if err != nil {
			return err
		}
		// Released int2_sum/int4_sum use an unchecked signed64 transition.
		s.sum += n
		s.seen = true
	}
	return ctx.Err()
}
func (p integralSumProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*integralSumState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid integral sum state")
	}
	if !s.seen {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Null(p.usage.Type), nil
	}
	value, err := sqltype.Integer(p.usage.Type, s.sum)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}

type numericSumProvider struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}

func (p numericSumProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p numericSumProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.input == nil || (p.input.ID() != sqltype.Int8 && p.input.ID() != sqltype.Numeric) || p.usage.Type == nil || p.usage.Type.ID() != sqltype.Numeric {
		return nil, fmt.Errorf("stuntgres: invalid numeric sum descriptor")
	}
	state := &sqltype.NumericSumState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p numericSumProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*sqltype.NumericSumState)
	if !ok || s == nil || value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid numeric sum state or datum")
	}
	if err := s.Add(value); err != nil {
		return err
	}
	return ctx.Err()
}
func (p numericSumProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*sqltype.NumericSumState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid numeric sum state")
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

type floatSumProvider struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}
type floatSumState struct {
	sum  float64
	seen bool
}

func (p floatSumProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p floatSumProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.input == nil || (p.input.ID() != sqltype.Float4 && p.input.ID() != sqltype.Float8) || p.usage.Type != p.input {
		return nil, fmt.Errorf("stuntgres: invalid float sum descriptor")
	}
	state := &floatSumState{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return state, nil
}
func (p floatSumProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*floatSumState)
	if !ok || s == nil || value.Type() == nil || value.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid float sum state or datum")
	}
	if value.IsNull() {
		return ctx.Err()
	}
	n, err := value.Float()
	if err != nil {
		return err
	}
	if !s.seen {
		s.sum, s.seen = n, true
		return ctx.Err()
	}
	var sum float64
	if p.input.ID() == sqltype.Float4 {
		sum = float64(float32(float32(s.sum) + float32(n)))
	} else {
		sum = s.sum + n
	}
	if math.IsInf(sum, 0) && !math.IsInf(s.sum, 0) && !math.IsInf(n, 0) {
		return sqlError("22003", "value out of range: overflow")
	}
	s.sum = sum
	return ctx.Err()
}
func (p floatSumProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*floatSumState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid float sum state")
	}
	if !s.seen {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Null(p.usage.Type), nil
	}
	// Already rounded at every transition; the constructor does no additional
	// arithmetic or narrowing, so it cannot invent an addition-underflow error.
	value, err := sqltype.Float(p.usage.Type, s.sum)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
