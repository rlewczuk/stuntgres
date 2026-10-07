package sqltype

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
)

// NumericRandom samples the inclusive decimal lattice at the greatest input
// display scale. All arithmetic is exact; mutable big integers never escape
// into the immutable result. The caller serializes its word source.
func NumericRandom(ctx context.Context, lower, upper Value, next func() uint64) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if lower.typ == nil || lower.typ.id != Numeric || upper.typ != lower.typ || lower.null || upper.null || next == nil {
		return Value{}, fmt.Errorf("invalid numeric random arguments")
	}
	for _, bound := range []struct {
		value Value
		name  string
	}{{lower, "lower"}, {upper, "upper"}} {
		if bound.value.special == numericNaN {
			return Value{}, failure("22023", bound.name+" bound cannot be NaN")
		}
		if bound.value.special != numericFinite {
			return Value{}, failure("22023", bound.name+" bound cannot be infinity")
		}
	}
	order := compareNumeric(lower, upper)
	if order > 0 {
		return Value{}, failure("22023", "lower bound must be less than or equal to upper bound")
	}
	scale := max(lower.scale, upper.scale)
	if order == 0 {
		lower.scale = scale
		return lower, nil
	}
	coefficient := func(v Value) big.Int {
		var n, power big.Int
		if v.text == "" {
			return n
		}
		n.SetString(v.text, 10)
		power.Exp(big.NewInt(10), big.NewInt(v.exponent+scale), nil)
		n.Mul(&n, &power)
		if v.negative {
			n.Neg(&n)
		}
		return n
	}
	lo := coefficient(lower)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	hi := coefficient(upper)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var width, candidate big.Int
	width.Sub(&hi, &lo)
	width.Add(&width, big.NewInt(1))
	// Sampling bitlen(width-1) bits avoids rejection for powers of two.
	candidate.Sub(&width, big.NewInt(1))
	bits := candidate.BitLen()
	words := (bits + 63) / 64
	if bits <= 64 {
		mask := ^uint64(0)
		if bits < 64 {
			mask = (uint64(1) << uint(bits)) - 1
		}
		limit := width.Uint64()
		for {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			x := next() & mask
			if width.BitLen() > 64 || x < limit {
				candidate.SetUint64(x)
				break
			}
		}
	} else {
		// Fill reusable magnitude words directly: repeated big.Int shifts would
		// copy the whole prefix for each random word at extreme numeric widths.
		scratch := make([]big.Word, (bits+strconv.IntSize-1)/strconv.IntSize)
		for {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			for i := range words {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				x := next()
				if i == 0 && bits%64 != 0 {
					x &= (uint64(1) << uint(bits%64)) - 1
				}
				slot := (words - 1 - i) * (64 / strconv.IntSize)
				scratch[slot] = big.Word(x)
				if strconv.IntSize == 32 && slot+1 < len(scratch) {
					scratch[slot+1] = big.Word(x >> 32)
				}
			}
			candidate.SetBits(scratch)
			if candidate.Cmp(&width) < 0 {
				break
			}
		}
	}
	candidate.Add(&candidate, &lo)
	negative := candidate.Sign() < 0
	candidate.Abs(&candidate)
	v := Value{typ: lower.typ, text: candidate.String(), exponent: -scale, scale: scale, negative: negative}
	return normalizeNumeric(v), nil
}
