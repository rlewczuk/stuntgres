package sqltype

import (
	"fmt"
	"math"
)

// FloatAvgState retains PostgreSQL's Youngs-Cramer transition, including Sxx:
// variance overflow can reject AVG even when the mean itself would be finite.
// States belong to a single group. Result does not mutate them.
type FloatAvgState struct{ n, sx, sxx float64 }

func (s *FloatAvgState) Add(v Value) error {
	if s == nil || v.typ == nil || !isFloat(v.typ.id) {
		return fmt.Errorf("invalid float average datum or state")
	}
	if v.null {
		return nil
	}
	x, err := v.Float()
	if err != nil {
		return err
	}
	n, sx, sxx := s.n+1, s.sx+x, s.sxx
	if s.n > 0 {
		// Explicit rounding of products forbids fused multiply/subtract and
		// preserves the released C evaluation order.
		product := float64(x * n)
		tmp := product - sx
		square := float64(tmp * tmp)
		denominator := float64(n * s.n)
		sxx += square / denominator
		if math.IsInf(sx, 0) || math.IsInf(sxx, 0) {
			if !math.IsInf(s.sx, 0) && !math.IsInf(x, 0) {
				return failure("22003", "value out of range: overflow")
			}
			sxx = math.NaN()
		}
	} else if math.IsNaN(x) || math.IsInf(x, 0) {
		sxx = math.NaN()
	}
	s.n, s.sx, s.sxx = n, sx, sxx
	return nil
}

func (s *FloatAvgState) Result(d *Descriptor) (Value, error) {
	if s == nil || d == nil || d.id != Float8 {
		return Value{}, fmt.Errorf("invalid float average descriptor or state")
	}
	if s.n == 0 {
		return Null(d), nil
	}
	// The final division deliberately permits IEEE underflow and signed zero.
	return Float(d, s.sx/s.n)
}
