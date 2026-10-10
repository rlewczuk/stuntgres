package sqltype

import (
	"context"
	"math"
	"math/big"
)

// Rounded decimal division at an explicit scale. Inputs and output may alias;
// scratch is exclusively local to one operation and the denominator is positive.
func (w *expScratch) divide(out, a, b *expDecimal, scale int64) {
	w.product.Set(&a.coefficient)
	w.divisor.Abs(&b.coefficient)
	if b.coefficient.Sign() < 0 {
		w.product.Neg(&w.product)
	}
	shift := a.exponent - b.exponent + scale
	if shift > 0 {
		w.product.Mul(&w.product, w.tenPower(shift))
	} else if shift < 0 {
		w.divisor.Mul(&w.divisor, w.tenPower(-shift))
	}
	sign := w.product.Sign()
	out.coefficient.QuoRem(&w.product, &w.divisor, &w.remainder)
	w.twice.Abs(&w.remainder)
	w.twice.Lsh(&w.twice, 1)
	if w.twice.Cmp(&w.divisor) >= 0 {
		w.twice.SetInt64(int64(sign))
		out.coefficient.Add(&out.coefficient, &w.twice)
	}
	out.exponent = -scale
}

func (w *expScratch) weight(x *expDecimal) int64 {
	if x.coefficient.Sign() == 0 {
		return 0
	}
	// All weight users here operate on nonnegative magnitudes.
	decimal := w.digits(&x.coefficient) + x.exponent - 1
	weight := decimal / 4
	if decimal < 0 && decimal%4 != 0 {
		weight--
	}
	return weight
}

func (w *expScratch) comparePositive(a, b *expDecimal) int {
	if a.coefficient.Sign() == 0 || b.coefficient.Sign() == 0 {
		return a.coefficient.Sign() - b.coefficient.Sign()
	}
	aw := w.digits(&a.coefficient) + a.exponent
	bw := w.digits(&b.coefficient) + b.exponent
	if aw < bw {
		return -1
	}
	if aw > bw {
		return 1
	}
	if a.exponent == b.exponent {
		return a.coefficient.Cmp(&b.coefficient)
	}
	if a.exponent > b.exponent {
		w.product.Mul(&a.coefficient, w.tenPower(a.exponent-b.exponent))
		return w.product.Cmp(&b.coefficient)
	}
	w.product.Mul(&b.coefficient, w.tenPower(b.exponent-a.exponent))
	return a.coefficient.Cmp(&w.product)
}

// Exact sqrt rounding, including negative target scales. floor(sqrt(N/D)) is
// sqrt(floor(N/D)); the original rational decides the midpoint, without binary
// arithmetic. big.Int.Sqrt is indivisible; cancellation is fenced around it.
func decimalSqrt(ctx context.Context, out, a *expDecimal, scale int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var numerator, denominator, quotient, root, threshold big.Int
	var w expScratch
	numerator.Set(&a.coefficient)
	denominator.SetInt64(1)
	shift := a.exponent + 2*scale
	if shift >= 0 {
		numerator.Mul(&numerator, w.tenPower(shift))
	} else {
		denominator.Set(w.tenPower(-shift))
	}
	quotient.Quo(&numerator, &denominator)
	if err := ctx.Err(); err != nil {
		return err
	}
	root.Sqrt(&quotient)
	if err := ctx.Err(); err != nil {
		return err
	}
	threshold.Lsh(&root, 1)
	threshold.Add(&threshold, big.NewInt(1))
	threshold.Mul(&threshold, &threshold)
	threshold.Mul(&threshold, &denominator)
	numerator.Lsh(&numerator, 2)
	if numerator.Cmp(&threshold) >= 0 {
		root.Add(&root, big.NewInt(1))
	}
	out.coefficient.Set(&root)
	out.exponent = -scale
	return ctx.Err()
}

// PostgreSQL ln_var: reduce with decimal square roots, then the atanh series.
// Caller has already enforced the selected power's zero/negative domain rules.
func decimalLn(ctx context.Context, out, a *expDecimal, scale int64) error {
	var x, low, high, one, denominator, z, square, term, factor expDecimal
	x.coefficient.Set(&a.coefficient)
	x.exponent = a.exponent
	low.coefficient.SetInt64(9)
	low.exponent = -1
	high.coefficient.SetInt64(11)
	high.exponent = -1
	one.coefficient.SetInt64(1)
	var w expScratch
	roots := int64(0)
	for w.comparePositive(&x, &low) <= 0 || w.comparePositive(&x, &high) >= 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		local := scale - w.weight(&x)*4/2 + 8
		if err := decimalSqrt(ctx, &x, &x, local); err != nil {
			return err
		}
		roots++
	}
	local := scale + int64(float64(roots+1)*0.301029995663981) + 8
	denominator.coefficient.Set(&x.coefficient)
	denominator.exponent = x.exponent
	w.add(&denominator, &one)
	one.coefficient.SetInt64(-1)
	w.add(&x, &one)
	w.divide(&z, &x, &denominator, local)
	out.coefficient.Set(&z.coefficient)
	out.exponent = z.exponent
	term.coefficient.Set(&z.coefficient)
	term.exponent = z.exponent
	w.multiply(&square, &z, &z, local)
	for divisor := int64(3); ; divisor += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.multiply(&term, &term, &square, local)
		w.divideInt(&x, &term, divisor, local)
		if x.coefficient.Sign() == 0 {
			break
		}
		w.add(out, &x)
		// REL_18_0 also stops terms far below the result's working significance.
		// Magnitude weights use absolute scratch, never signed digit estimates.
		w.twice.Abs(&x.coefficient)
		termWeight := w.digits(&w.twice) + x.exponent - 1
		w.twice.Abs(&out.coefficient)
		resultWeight := w.digits(&w.twice) + out.exponent - 1
		tw := int64(math.Floor(float64(termWeight) / 4))
		rw := int64(math.Floor(float64(resultWeight) / 4))
		if tw < rw-local*2/4 {
			break
		}
	}
	factor.coefficient.Lsh(big.NewInt(1), uint(roots+1))
	w.multiply(out, out, &factor, scale)
	return ctx.Err()
}
