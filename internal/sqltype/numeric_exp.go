package sqltype

import (
	"context"
	"math"
	"math/big"
	"strconv"
)

// expDecimal and expScratch are operation-local exact decimal arithmetic used
// by exp and numeric power. No mutable coefficient escapes into a Value.
// Precision schedules follow PostgreSQL REL_18_0 numeric.c.
type expDecimal struct {
	coefficient big.Int
	exponent    int64
}

type expScratch struct {
	product, power, remainder, twice, divisor big.Int
	powerExponent                             int64
	powerValid                                bool
}

func (w *expScratch) tenPower(n int64) *big.Int {
	if !w.powerValid || w.powerExponent != n {
		w.power.Exp(big.NewInt(10), big.NewInt(n), nil)
		w.powerExponent, w.powerValid = n, true
	}
	return &w.power
}

// Round a signed decimal coefficient half away from zero. Retain its exponent
// when no digits need discarding instead of manufacturing unnecessary zeros.
func (w *expScratch) round(out *expDecimal, n *big.Int, exponent, scale int64) {
	cut := -exponent - scale
	if cut <= 0 {
		out.coefficient.Set(n)
		out.exponent = exponent
		return
	}
	// A conservative digit upper bound makes far-underflow rounding constant
	// space; do not expand thousands of virtual zeros for a tiny coefficient.
	if n.Sign() == 0 || cut > int64(float64(n.BitLen())*0.301029995663982)+1 {
		out.coefficient.SetInt64(0)
		out.exponent = -scale
		return
	}
	sign := n.Sign()
	out.coefficient.QuoRem(n, w.tenPower(cut), &w.remainder)
	w.twice.Abs(&w.remainder)
	w.twice.Lsh(&w.twice, 1)
	if w.twice.Cmp(&w.power) >= 0 {
		w.twice.SetInt64(int64(sign))
		out.coefficient.Add(&out.coefficient, &w.twice)
	}
	out.exponent = -scale
}

func (w *expScratch) multiply(out, a, b *expDecimal, scale int64) {
	w.product.Mul(&a.coefficient, &b.coefficient)
	w.round(out, &w.product, a.exponent+b.exponent, scale)
}

func (w *expScratch) divideInt(out, a *expDecimal, divisor, scale int64) {
	w.product.Set(&a.coefficient)
	w.divisor.SetInt64(divisor)
	shift := a.exponent + scale
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

func (w *expScratch) add(out, b *expDecimal) {
	if out.exponent <= b.exponent {
		w.product.Set(&b.coefficient)
		if out.exponent < b.exponent {
			w.product.Mul(&w.product, w.tenPower(b.exponent-out.exponent))
		}
		out.coefficient.Add(&out.coefficient, &w.product)
	} else {
		out.coefficient.Mul(&out.coefficient, w.tenPower(out.exponent-b.exponent))
		out.coefficient.Add(&out.coefficient, &b.coefficient)
		out.exponent = b.exponent
	}
}

// BitLen supplies a lower-bound decimal digit count. The exact comparison
// corrects its possible one-digit underestimate without formatting a large
// intermediate result at each squaring.
func (w *expScratch) digits(n *big.Int) int64 {
	if n.Sign() == 0 {
		return 1
	}
	digits := int64(float64(n.BitLen()-1)*0.3010299956639812) + 1
	if n.Cmp(w.tenPower(digits)) >= 0 {
		digits++
	}
	return digits
}

func numericExp(ctx context.Context, a Value) (Value, error) {
	switch a.special {
	case numericNaN, numericInfinity:
		return a, nil
	case numericNegativeInfinity:
		return Value{typ: a.typ}, nil
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	// Approximation selects display precision and the release's +/-6000
	// guard only. Decimal arithmetic below never uses this binary result.
	// Scientific input avoids padding virtual zeros; ParseFloat's range
	// error is intentionally ignored here (very large inputs estimate Inf).
	estimate := float64(0)
	if a.text != "" {
		text := a.text + "e" + strconv.FormatInt(a.exponent, 10)
		estimate, _ = strconv.ParseFloat(text, 64)
		if a.negative {
			estimate = -estimate
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	weight := int64(math.Max(-2000, math.Min(2000, estimate*0.434294481903252)))
	scale := min(int64(1000), max(int64(0), a.scale, 16-weight))
	if estimate >= 6000 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	if estimate <= -6000 {
		return Value{typ: a.typ, scale: scale}, nil
	}
	if a.text == "" {
		return Value{typ: a.typ, text: "1", scale: scale}, nil
	}
	var x, result expDecimal
	x.setValue(a)
	if err := decimalExp(ctx, &x, &result, scale); err != nil {
		return Value{}, err
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

// decimalExp consumes operation-local x and computes exp(x) at the requested
// scale. SQL exp and general power share this exact kernel, not rounded SQL
// results. Estimates select precision only; all result arithmetic is decimal.
func decimalExp(ctx context.Context, x, result *expDecimal, scale int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	estimate := decimalEstimate(x)
	if estimate >= 6000 {
		return failure("22003", "value overflows numeric format")
	}
	if estimate <= -6000 {
		result.coefficient.SetInt64(0)
		result.exponent = -scale
		return ctx.Err()
	}
	if x.coefficient.Sign() == 0 {
		result.coefficient.SetInt64(1)
		result.exponent = 0
		return ctx.Err()
	}
	var term expDecimal
	var w expScratch
	k := int64(0)
	for math.Abs(estimate) > 0.01 {
		estimate *= 0.5
		k++
	}
	if k != 0 {
		// Dividing by 2^k is exact: multiply by 5^k and shift by k
		// decimal places. Even maximum input scale stays bounded here.
		w.power.Exp(big.NewInt(5), big.NewInt(k), nil)
		x.coefficient.Mul(&x.coefficient, &w.power)
		x.exponent -= k
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dweight := int64(math.Trunc(estimate * math.Ldexp(1, int(k)) * 0.434294481903252))
	// Eight guard digits protect Taylor rounding and its amplification
	// through k squarings; precision also includes the reduction factor.
	significant := max(int64(0), 1+dweight+scale+int64(float64(k)*0.301029995663981)) + 8
	localScale := significant - 1
	result.coefficient.SetInt64(1)
	result.exponent = 0
	w.add(result, x)
	w.multiply(&term, x, x, localScale)
	w.divideInt(&term, &term, 2, localScale)
	for divisor := int64(3); term.coefficient.Sign() != 0; divisor++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.add(result, &term)
		w.multiply(&term, &term, x, localScale)
		w.divideInt(&term, &term, divisor, localScale)
	}
	// |x| <= .01 guarantees decaying terms. Termination is at the decimal
	// precision selected above, not a float epsilon or arbitrary term cap.
	for range k {
		if err := ctx.Err(); err != nil {
			return err
		}
		decimalWeight := w.digits(&result.coefficient) + result.exponent - 1
		baseWeight := decimalWeight / 4
		if decimalWeight < 0 && decimalWeight%4 != 0 {
			baseWeight-- // mathematical floor, not Go's truncation
		}
		localScale = max(int64(0), significant-2*baseWeight*4)
		w.multiply(result, result, result, localScale)
	}
	w.round(result, &result.coefficient, result.exponent, scale)
	return ctx.Err()
}

func decimalEstimate(x *expDecimal) float64 {
	if x.coefficient.Sign() == 0 {
		return 0
	}
	text := x.coefficient.String() + "e" + strconv.FormatInt(x.exponent, 10)
	estimate, _ := strconv.ParseFloat(text, 64)
	return estimate
}
