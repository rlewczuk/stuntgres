package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type IntegralBitwiseAggregateProvider struct {
	usage     sqltype.Usage
	operation sqltype.BitwiseOperator
	bound     sqltype.Bitwise
}

type integralBitwiseAggregateState struct {
	seen  bool
	value sqltype.Value
}

func (p IntegralBitwiseAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p IntegralBitwiseAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &integralBitwiseAggregateState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p IntegralBitwiseAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*integralBitwiseAggregateState)
	if !ok || s == nil || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid integral bitwise aggregate state")
	}
	if value.Type() != p.usage.Type || value.IsNull() {
		return fmt.Errorf("stuntgres: invalid integral bitwise aggregate datum")
	}
	if !s.seen {
		s.seen, s.value = true, value
	} else {
		v, err := p.bound.Binary(ctx, s.value, value)
		if err != nil {
			return err
		}
		s.value = v
	}
	return ctx.Err()
}
func (p IntegralBitwiseAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*integralBitwiseAggregateState)
	if !ok || s == nil || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid integral bitwise aggregate state")
	}
	return s.value, nil
}

type BitStringAggregateProvider struct {
	usage     sqltype.Usage
	operation sqltype.BitwiseOperator
}

type bitStringAggregateState struct {
	seen bool
	bits []byte
}

func (p BitStringAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p BitStringAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &bitStringAggregateState{}, nil
}
func (p BitStringAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*bitStringAggregateState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid bit string aggregate state")
	}
	bits, err := value.Bits()
	if err != nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid bit string aggregate datum")
	}
	if !s.seen {
		s.bits = []byte(bits)
		s.seen = true
		return ctx.Err()
	}
	if len(s.bits) != len(bits) {
		operation := "AND"
		if p.operation == sqltype.BitwiseOr {
			operation = "OR"
		}
		if p.operation == sqltype.BitwiseXor {
			operation = "XOR"
		}
		return sqlError("22026", "cannot "+operation+" bit strings of different sizes")
	}
	return p.fold(ctx, s, bits)
}

func (p BitStringAggregateProvider) fold(ctx context.Context, s *bitStringAggregateState, bits string) error {
	// Scratch is group-owned. Poll within each actual fold, including absorbing
	// AND/OR states; neither value evaluation nor width checking is short-circuited.
	switch p.operation {
	case sqltype.BitwiseAnd:
		for i := range len(bits) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if bits[i] == '0' {
				s.bits[i] = '0'
			}
		}
	case sqltype.BitwiseOr:
		for i := range len(bits) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if bits[i] == '1' {
				s.bits[i] = '1'
			}
		}
	case sqltype.BitwiseXor:
		for i := range len(bits) {
			if i&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if s.bits[i] == bits[i] {
				s.bits[i] = '0'
			} else {
				s.bits[i] = '1'
			}
		}
	default:
		return fmt.Errorf("stuntgres: invalid bit string aggregate operation")
	}
	return ctx.Err()
}
func (p BitStringAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*bitStringAggregateState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid bit string aggregate state")
	}
	if !s.seen {
		return sqltype.Null(p.usage.Type), nil
	}
	// Safe conversion seals the result: later transitions cannot mutate it.
	value, err := sqltype.BitString(p.usage.Type, string(s.bits))
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
func NewIntegralBitwiseAggregateProvider(usage sqltype.Usage, operation sqltype.BitwiseOperator, bound sqltype.Bitwise) IntegralBitwiseAggregateProvider {
	return IntegralBitwiseAggregateProvider{usage, operation, bound}
}
func NewBitStringAggregateProvider(usage sqltype.Usage, operation sqltype.BitwiseOperator) BitStringAggregateProvider {
	return BitStringAggregateProvider{usage, operation}
}
func (p IntegralBitwiseAggregateProvider) Operator() sqltype.BitwiseOperator { return p.operation }
func (p BitStringAggregateProvider) Operator() sqltype.BitwiseOperator       { return p.operation }
