package sqltype

import (
	"fmt"
	"math/big"
)

// NumericAvgState owns an exact decimal sum and denominator for one group.
// The accumulator may exceed the packed NUMERIC range until Result. Scratch
// integers never escape into immutable Values; the zero state is empty.
type NumericAvgState struct {
	sum, count                              big.Int
	exponent, scale                         int64
	nan, positiveInfinity, negativeInfinity bool
	datum, power, powerExponent             big.Int
	powerScale                              int64
	numerator, divisor, quotient, remainder big.Int
}

func (s *NumericAvgState) pow10(n int64) *big.Int {
	if s.power.Sign() == 0 || s.powerScale != n {
		s.power.SetInt64(10)
		s.powerExponent.SetInt64(n)
		s.power.Exp(&s.power, &s.powerExponent, nil)
		s.powerScale = n
	}
	return &s.power
}

func (s *NumericAvgState) Add(v Value) error {
	if s == nil || v.typ == nil || !exactNumber(v.typ.id) {
		return fmt.Errorf("invalid numeric average datum or state")
	}
	if v.null {
		return nil
	}
	if v.typ.id == Numeric {
		switch v.special {
		case numericNaN:
			s.nan = true
			return nil
		case numericInfinity:
			s.positiveInfinity = true
			return nil
		case numericNegativeInfinity:
			s.negativeInfinity = true
			return nil
		}
	}
	// Reuse the same small integer instead of constructing an exactOperand.
	s.datum.SetInt64(1)
	s.count.Add(&s.count, &s.datum)
	exponent := int64(0)
	if v.typ.id == Numeric {
		if v.scale > s.scale {
			s.scale = v.scale
		}
		exponent = v.exponent
		if v.text == "" {
			return nil
		}
		if _, ok := s.datum.SetString(v.text, 10); !ok {
			return fmt.Errorf("invalid numeric average coefficient")
		}
		if v.negative {
			s.datum.Neg(&s.datum)
		}
	} else {
		s.datum.SetInt64(v.integer)
		if v.integer == 0 {
			return nil
		}
	}
	if s.sum.Sign() == 0 {
		s.sum.Set(&s.datum)
		s.exponent = exponent
		return nil
	}
	if exponent < s.exponent {
		s.sum.Mul(&s.sum, s.pow10(s.exponent-exponent))
		s.exponent = exponent
	} else if exponent > s.exponent {
		s.datum.Mul(&s.datum, s.pow10(exponent-s.exponent))
	}
	s.sum.Add(&s.sum, &s.datum)
	return nil
}

// numericAvgLeading reads the normalized base-10000 weight/leading digit
// directly from coefficient digits, without building padded numeric output.
func numericAvgLeading(digits string, exponent int64) (int64, int64) {
	if digits == "" {
		return 0, 0
	}
	position := int64(len(digits)) + exponent
	weight := (position - 1) / 4
	if position-1 < 0 && (position-1)%4 != 0 {
		weight--
	}
	width := position - 4*weight
	first := int64(0)
	for i := int64(0); i < width; i++ {
		first *= 10
		if i < int64(len(digits)) {
			first += int64(digits[i] - '0')
		}
	}
	return weight, first
}

func (s *NumericAvgState) Result(d *Descriptor) (Value, error) {
	if s == nil || d == nil || d.id != Numeric {
		return Value{}, fmt.Errorf("invalid numeric average descriptor or state")
	}
	v := Value{typ: d}
	switch {
	case s.nan || s.positiveInfinity && s.negativeInfinity:
		v.special = numericNaN
		return v, nil
	case s.positiveInfinity:
		v.special = numericInfinity
		return v, nil
	case s.negativeInfinity:
		v.special = numericNegativeInfinity
		return v, nil
	case s.count.Sign() == 0:
		return Null(d), nil
	}
	s.numerator.Abs(&s.sum)
	if s.numerator.Sign() != 0 {
		v.text = s.numerator.String()
	}
	v.exponent, v.scale = s.exponent, s.scale
	v = normalizeNumeric(v)
	// PostgreSQL packs the finite SUM before division, not just the mean.
	if v.scale > 16383 || v.text != "" && int64(len(v.text))+v.exponent > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	weight, first := numericAvgLeading(v.text, v.exponent)
	countWeight, countFirst := numericAvgLeading(s.count.String(), 0)
	qweight := weight - countWeight
	if first <= countFirst {
		qweight--
	}
	scale := int64(16) - 4*qweight
	if scale < 0 {
		scale = 0
	}
	if scale < s.scale {
		scale = s.scale
	}
	if scale > 1000 {
		scale = 1000
	}
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
