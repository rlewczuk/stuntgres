package sqltype

import (
	"fmt"
	"math/big"
)

// NumericAvgState shares exact accumulation/packing with SUM and owns only
// AVG's finite denominator and division scratch in addition.
type NumericAvgState struct {
	NumericSumState
	count                                   big.Int
	numerator, divisor, quotient, remainder big.Int
}

func (s *NumericAvgState) Add(v Value) error {
	if s == nil {
		return fmt.Errorf("invalid numeric average state")
	}
	if err := s.NumericSumState.Add(v); err != nil {
		return err
	}
	if !v.null && (v.typ.id != Numeric || v.special == numericFinite) {
		s.datum.SetInt64(1)
		s.count.Add(&s.count, &s.datum)
	}
	return nil
}

func (s *NumericAvgState) Result(d *Descriptor) (Value, error) {
	if s == nil || d == nil || d.id != Numeric {
		return Value{}, fmt.Errorf("invalid numeric average descriptor or state")
	}
	v, err := s.NumericSumState.Result(d)
	if err != nil || v.null || v.special != numericFinite {
		return v, err
	}
	s.numerator.Abs(&s.sum)
	scale := numericDivisionScale(v.text, v.exponent, s.scale, s.count.String(), 0, 0)
	if s.sum.Sign() == 0 {
		return Value{typ: d, scale: scale}, nil
	}
	s.divisor.Set(&s.count)
	shift := s.exponent + scale
	if shift >= 0 {
		s.numerator.Mul(&s.numerator, s.pow10(shift))
	} else {
		s.divisor.Mul(&s.divisor, s.pow10(-shift))
	}
	s.quotient.QuoRem(&s.numerator, &s.divisor, &s.remainder)
	s.remainder.Lsh(&s.remainder, 1)
	if s.remainder.Cmp(&s.divisor) >= 0 {
		s.datum.SetInt64(1)
		s.quotient.Add(&s.quotient, &s.datum)
	}
	v = Value{typ: d, exponent: -scale, scale: scale, negative: s.sum.Sign() < 0}
	if s.quotient.Sign() != 0 {
		v.text = s.quotient.String()
	}
	v = normalizeNumeric(v)
	if v.text != "" && int64(len(v.text))+v.exponent > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	return v, nil
}
