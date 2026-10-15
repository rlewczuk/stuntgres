package sqltype

import (
	"fmt"
	"math"
)

// RegressionOperation identifies PostgreSQL floating regression finalizers.
type RegressionOperation uint8

const (
	RegressionSlope RegressionOperation = iota
	RegressionSxx
	RegressionSxy
	RegressionSyy
	RegressionCorr
	RegressionCovarPop
	RegressionCovarSamp
	RegressionAvgx
	RegressionAvgy
	RegressionIntercept
	RegressionR2
)

// FloatRegressionState is REL_18_0's six-field Youngs-Cramer state, not the
// newer constant-axis correction. Y precedes X at the SQL and state boundary.
type FloatRegressionState struct{ n, sx, sxx, sy, syy, sxy float64 }

func (s *FloatRegressionState) Add(y, x Value) error {
	if s == nil || y.typ == nil || x.typ == nil || y.typ.id != Float8 || x.typ.id != Float8 {
		return fmt.Errorf("invalid regression datum or state")
	}
	if y.null || x.null {
		return nil
	}
	xv, err := x.Float()
	if err != nil {
		return err
	}
	yv, err := y.Float()
	if err != nil {
		return err
	}
	n, sx, sy := s.n+1, s.sx+xv, s.sy+yv
	sxx, syy, sxy := s.sxx, s.syy, s.sxy
	if s.n > 0 {
		// Explicit casts round each product and prohibit fused arithmetic.
		tx := float64(xv*n) - sx
		ty := float64(yv*n) - sy
		scale := 1 / float64(n*s.n)
		sxx += float64(float64(tx*tx) * scale)
		syy += float64(float64(ty*ty) * scale)
		sxy += float64(float64(tx*ty) * scale)
		if math.IsInf(sx, 0) || math.IsInf(sxx, 0) || math.IsInf(sy, 0) || math.IsInf(syy, 0) || math.IsInf(sxy, 0) {
			finiteX := !math.IsInf(s.sx, 0) && !math.IsInf(xv, 0)
			finiteY := !math.IsInf(s.sy, 0) && !math.IsInf(yv, 0)
			if (math.IsInf(sx, 0) || math.IsInf(sxx, 0)) && finiteX || (math.IsInf(sy, 0) || math.IsInf(syy, 0)) && finiteY || math.IsInf(sxy, 0) && finiteX && finiteY {
				return failure("22003", "value out of range: overflow")
			}
			if math.IsInf(sxx, 0) {
				sxx = math.NaN()
			}
			if math.IsInf(syy, 0) {
				syy = math.NaN()
			}
			if math.IsInf(sxy, 0) {
				sxy = math.NaN()
			}
		}
	} else {
		if math.IsNaN(xv) || math.IsInf(xv, 0) {
			sxx, sxy = math.NaN(), math.NaN()
		}
		if math.IsNaN(yv) || math.IsInf(yv, 0) {
			syy, sxy = math.NaN(), math.NaN()
		}
	}
	// Failed transitions never partially update even an unrequested axis.
	s.n, s.sx, s.sxx, s.sy, s.syy, s.sxy = n, sx, sxx, sy, syy, sxy
	return nil
}

func (s *FloatRegressionState) Result(d *Descriptor, operation RegressionOperation) (Value, error) {
	if s == nil || d == nil || d.id != Float8 || operation > RegressionR2 {
		return Value{}, fmt.Errorf("invalid regression descriptor, operation or state")
	}
	if s.n == 0 {
		return Null(d), nil
	}
	var result float64
	switch operation {
	case RegressionSlope:
		if s.sxx == 0 {
			return Null(d), nil
		}
		result = s.sxy / s.sxx
	case RegressionSxx:
		result = s.sxx
	case RegressionSxy:
		result = s.sxy
	case RegressionSyy:
		result = s.syy
	case RegressionCorr:
		if s.sxx == 0 || s.syy == 0 {
			return Null(d), nil
		}
		result = s.sxy / math.Sqrt(float64(s.sxx*s.syy))
	case RegressionCovarPop:
		result = s.sxy / s.n
	case RegressionCovarSamp:
		if s.n < 2 {
			return Null(d), nil
		}
		result = s.sxy / (s.n - 1)
	case RegressionAvgx:
		result = s.sx / s.n
	case RegressionAvgy:
		result = s.sy / s.n
	case RegressionIntercept:
		if s.sxx == 0 {
			return Null(d), nil
		}
		result = (s.sy - float64(s.sx*s.sxy)/s.sxx) / s.n
	case RegressionR2:
		if s.sxx == 0 {
			return Null(d), nil
		}
		if s.syy == 0 {
			result = 1
		} else {
			result = float64(s.sxy*s.sxy) / float64(s.sxx*s.syy)
		}
	}
	return Float(d, result)
}
