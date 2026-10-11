//go:build reference

package reference_test

import (
	"math/big"
	"stuntgres/reference/fixture"
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
	p := fixture.NewPair(t)
	want := referenceScalarProduct(2, 32177).String()
	s := fixture.Select("SELECT factorial(32177)", []string{"factorial"}, []string{"NUMERIC"}, [][]any{{want}})
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
