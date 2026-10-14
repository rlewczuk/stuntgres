package sqltype

import (
	"context"
	"fmt"
	"math/big"
)

// NumericMomentsState owns exact, unpacked sums and squares. No intermediate
// packed Numeric or binary float can lose integer precision or reject cancellation.
type NumericMomentsState struct {
	sum                              NumericSumState
	count                            int64
	squares, square, countInt        big.Int
	squareExponent                   int64
	numerator, denominator, variance expDecimal
	work                             expScratch
}

func (s *NumericMomentsState) Add(ctx context.Context, v Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || v.typ == nil || !exactNumber(v.typ.id) {
		return fmt.Errorf("invalid numeric moments datum or state")
	}
	if v.null {
		return nil
	}
	finite, err := s.sum.prepareDatum(v)
	if err != nil {
		return err
	}
	s.count++
	if !finite {
		return ctx.Err()
	}
	exponent := int64(0)
	if v.typ.id == Numeric {
		exponent = 2 * v.exponent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Square before sum alignment can rescale the parsed coefficient scratch.
	s.square.Mul(&s.sum.datum, &s.sum.datum)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.sum.addDatum(exponent / 2)
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.squares.Sign() == 0 {
		s.squares.Set(&s.square)
		s.squareExponent = exponent
	} else {
		if exponent < s.squareExponent {
			s.squares.Mul(&s.squares, s.sum.pow10(s.squareExponent-exponent))
			s.squareExponent = exponent
		} else if exponent > s.squareExponent {
			s.square.Mul(&s.square, s.sum.pow10(exponent-s.squareExponent))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.squares.Add(&s.squares, &s.square)
	}
	return ctx.Err()
}

func (s *NumericMomentsState) Result(ctx context.Context, d *Descriptor, sample, stddev bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if s == nil || d == nil || d.id != Numeric {
		return Value{}, fmt.Errorf("invalid numeric moments descriptor or state")
	}
	if s.count == 0 || sample && s.count <= 1 {
		return Null(d), nil
	}
	if s.sum.nan || s.sum.positiveInfinity || s.sum.negativeInfinity {
		return Value{typ: d, special: numericNaN}, nil
	}
	s.countInt.SetInt64(s.count)
	s.numerator.coefficient.Mul(&s.squares, &s.countInt)
	s.numerator.exponent = s.squareExponent
	s.square.Mul(&s.sum.sum, &s.sum.sum)
	sumExponent := 2 * s.sum.exponent
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if sumExponent < s.numerator.exponent {
		s.numerator.coefficient.Mul(&s.numerator.coefficient, s.sum.pow10(s.numerator.exponent-sumExponent))
		s.numerator.exponent = sumExponent
	} else if sumExponent > s.numerator.exponent {
		s.square.Mul(&s.square, s.sum.pow10(sumExponent-s.numerator.exponent))
	}
	s.numerator.coefficient.Sub(&s.numerator.coefficient, &s.square)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if s.numerator.coefficient.Sign() <= 0 {
		return Value{typ: d}, nil
	}
	other := s.count
	if sample {
		other--
	}
	s.denominator.coefficient.SetInt64(other)
	s.denominator.coefficient.Mul(&s.denominator.coefficient, &s.countInt)
	s.denominator.exponent = 0
	scale := numericDivisionScale(s.numerator.coefficient.String(), s.numerator.exponent, 2*s.sum.scale, s.denominator.coefficient.String(), 0, 0)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	s.work.divide(&s.variance, &s.numerator, &s.denominator, scale)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if stddev {
		if err := decimalSqrt(ctx, &s.variance, &s.variance, scale); err != nil {
			return Value{}, err
		}
	}
	return numericDecimalResult(ctx, d, &s.variance, scale)
}
