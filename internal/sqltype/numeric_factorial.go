package sqltype

import (
	"context"
	"math/big"
)

func numericFactorial(ctx context.Context, result *Descriptor, n int64) (Value, error) {
	if n < 0 {
		return Value{}, failure("22003", "factorial of a negative number is undefined")
	}
	// PostgreSQL's largest supported factorial is checked before allocating
	// its coefficient, not after doing an unbounded multiplication loop.
	if n > 32177 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	var product, factor big.Int
	product.SetInt64(1)
	for i := int64(2); i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		factor.SetInt64(i)
		product.Mul(&product, &factor)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	digits := product.String()
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return normalizeNumeric(Value{typ: result, text: digits}), nil
}
