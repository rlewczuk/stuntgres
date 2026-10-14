package sqltype

import (
	"fmt"
	"math/big"
)

// NumericSumState owns one group's exact decimal accumulation. Finite packing
// is deferred until Result so oversized transient subtotals can cancel. Scratch
// integers never escape into immutable Values; repeated Result is non-destructive.
// AVG reuses this state but owns its denominator and division scratch separately.
type NumericSumState struct {
	sum                                     big.Int
	exponent, scale                         int64
	seen                                    bool
	nan, positiveInfinity, negativeInfinity bool
	datum, power, powerExponent             big.Int
	powerScale                              int64
}

func (s *NumericSumState) pow10(n int64) *big.Int {
	if s.power.Sign() == 0 || s.powerScale != n {
		s.power.SetInt64(10)
		s.powerExponent.SetInt64(n)
		s.power.Exp(&s.power, &s.powerExponent, nil)
		s.powerScale = n
	}
	return &s.power
}

func (s *NumericSumState) Add(v Value) error {
	finite, err := s.prepareDatum(v)
	if err != nil || !finite {
		return err
	}
	exponent := int64(0)
	if v.typ.id == Numeric {
		exponent = v.exponent
	}
	s.addDatum(exponent)
	return nil
}

// prepareDatum tracks admission and parses one signed finite coefficient.
// Consumers must use the original coefficient before addDatum aligns scratch.
func (s *NumericSumState) prepareDatum(v Value) (bool, error) {
	if s == nil || v.typ == nil || !exactNumber(v.typ.id) {
		return false, fmt.Errorf("invalid numeric sum datum or state")
	}
	if v.null {
		return false, nil
	}
	if v.typ.id == Numeric {
		switch v.special {
		case numericNaN:
			s.nan = true
			return false, nil
		case numericInfinity:
			s.positiveInfinity = true
			return false, nil
		case numericNegativeInfinity:
			s.negativeInfinity = true
			return false, nil
		}
	}
	s.seen = true
	if v.typ.id == Numeric {
		if v.scale > s.scale {
			s.scale = v.scale
		}
		if v.text == "" {
			return false, nil
		}
		if _, ok := s.datum.SetString(v.text, 10); !ok {
			return false, fmt.Errorf("invalid numeric sum coefficient")
		}
		if v.negative {
			s.datum.Neg(&s.datum)
		}
	} else {
		s.datum.SetInt64(v.integer)
		if v.integer == 0 {
			return false, nil
		}
	}
	return true, nil
}

func (s *NumericSumState) addDatum(exponent int64) {
	if s.sum.Sign() == 0 {
		s.sum.Set(&s.datum)
		s.exponent = exponent
		return
	}
	if exponent < s.exponent {
		s.sum.Mul(&s.sum, s.pow10(s.exponent-exponent))
		s.exponent = exponent
	} else if exponent > s.exponent {
		s.datum.Mul(&s.datum, s.pow10(exponent-s.exponent))
	}
	s.sum.Add(&s.sum, &s.datum)
}

func (s *NumericSumState) Result(d *Descriptor) (Value, error) {
	if s == nil || d == nil || d.id != Numeric {
		return Value{}, fmt.Errorf("invalid numeric sum descriptor or state")
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
	case !s.seen:
		return Null(d), nil
	}
	// String's minus sign is detached from the immutable magnitude; never negate
	// the accumulator or use its datum scratch while finalizing.
	digits := s.sum.String()
	if s.sum.Sign() != 0 {
		if s.sum.Sign() < 0 {
			v.negative = true
			digits = digits[1:]
		}
		v.text = digits
	}
	v.exponent, v.scale = s.exponent, s.scale
	v = normalizeNumeric(v)
	if v.scale > 16383 || v.text != "" && int64(len(v.text))+v.exponent > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	return v, nil
}
