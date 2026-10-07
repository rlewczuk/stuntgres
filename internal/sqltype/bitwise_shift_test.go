package sqltype

import (
	"context"
	"math/big"
	"testing"
)

// Independent arbitrary-precision model: multiply/divide by powers of two,
// then reduce to signed storage width. No evaluator-produced expectations.
func TestBitwiseShiftGrid(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id             ID
		width, modulus int
		min, max       int64
	}{
		{Int2, 16, 32, -32768, 32767}, {Int4, 32, 32, -2147483648, 2147483647}, {Int8, 64, 64, -9223372036854775808, 9223372036854775807},
	} {
		storage := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(tc.width)), nil)
		half := new(big.Int).Quo(new(big.Int).Set(storage), big.NewInt(2))
		for _, x := range []int64{1, -1, tc.min, tc.max} {
			for _, count := range []int64{-2147483648, -65, -64, -33, -32, -1, 0, 1, 15, 16, 31, 32, 33, 63, 64, 65, 2147483647} {
				shift := count % int64(tc.modulus)
				if shift < 0 {
					shift += int64(tc.modulus)
				}
				factor := new(big.Int).Exp(big.NewInt(2), big.NewInt(shift), nil)
				for _, op := range []BitwiseOperator{BitwiseShiftLeft, BitwiseShiftRight} {
					expected := big.NewInt(x)
					if op == BitwiseShiftLeft {
						expected.Mul(expected, factor)
					} else {
						expected.Div(expected, factor)
					}
					expected.Mod(expected, storage)
					if expected.Cmp(half) >= 0 {
						expected.Sub(expected, storage)
					}
					p, err := BindBitwise(op, types[tc.id], types[Int4])
					if err != nil {
						t.Fatal(err)
					}
					a, _ := Integer(types[tc.id], x)
					b, _ := Integer(types[Int4], count)
					v, err := p.Binary(context.Background(), a, b)
					n, _ := v.Integer()
					if err != nil || n != expected.Int64() {
						t.Fatal(tc.id, x, count, op, n, expected, err)
					}
				}
			}
		}
	}
}
