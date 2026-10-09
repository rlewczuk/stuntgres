//go:build reference

package reference_test

import (
	"math/big"
	"testing"
)

func referenceScalarProduct(lo, hi int64) *big.Int {
	if lo > hi {
		return big.NewInt(1)
	}
	if lo == hi {
		return big.NewInt(lo)
	}
	mid := (lo + hi) / 2
	return new(big.Int).Mul(referenceScalarProduct(lo, mid), referenceScalarProduct(mid+1, hi))
}

func TestReferenceMathScalarFactorialMaximum(t *testing.T) {
	p := newReferencePair(t)
	want := referenceScalarProduct(2, 32177).String()
	s := referenceSelect("SELECT factorial(32177)", []string{"factorial"}, []string{"NUMERIC"}, [][]any{{want}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
}
