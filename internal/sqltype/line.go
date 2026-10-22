package sqltype

import (
	"fmt"
	"math"
	"stuntgres/internal/sqlstate"
)

func (d *Descriptor) lineInput(text string, env InputEnvironment) (Value, error) {
	s := cubeScanner{text: text}
	if !s.take('{') {
		return d.geometryInput(text, env)
	}
	invalid := func() error { return failure("22P02", fmt.Sprintf("invalid input syntax for type line: \"%s\"", text)) }
	var c [4]float64
	for i := range 3 {
		f := Descriptor{id: Float8}
		v, err := f.floatInput(s.token())
		if err != nil {
			if e, ok := err.(*sqlstate.Error); ok && e.Code == "22P02" {
				return Value{}, invalid()
			}
			return Value{}, err
		}
		c[i] = v.floating
		if i < 2 && !s.take(',') {
			return Value{}, invalid()
		}
	}
	if !s.take('}') {
		return Value{}, invalid()
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, invalid()
	}
	if boxFuzzyEqual(c[0], 0) && boxFuzzyEqual(c[1], 0) {
		return Value{}, failure("22P02", "invalid line specification: A and B cannot both be zero")
	}
	return Value{typ: d, geometry: &geometryPayload{coordinates: c}}, nil
}

// Reuse the ordinary float8 arithmetic checks, including subnormal underflow.
func lineArithmetic(op ArithmeticOperator, x, y float64) (float64, error) {
	return checkedFloatingBinary(op, Float8, x, y)
}

func (d *Descriptor) lineFromPoints(p [4]float64) (Value, error) {
	return d.lineFromPointsWithCode(p, "22P02")
}

// The text codec and SQL constructor share arithmetic, not coincidence SQLSTATE.
func (d *Descriptor) lineFromPointsWithCode(p [4]float64, code string) (Value, error) {
	equal := boxFuzzyEqual(p[0], p[2]) && boxFuzzyEqual(p[1], p[3])
	if math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsNaN(p[2]) || math.IsNaN(p[3]) {
		equal = compareFloats(p[0], p[2]) == 0 && compareFloats(p[1], p[3]) == 0
	}
	if equal {
		return Value{}, failure(code, "invalid line specification: must be two distinct points")
	}
	var c [4]float64
	switch {
	case boxFuzzyEqual(p[0], p[2]):
		c = [4]float64{-1, 0, p[0], 0}
	case boxFuzzyEqual(p[1], p[3]):
		c = [4]float64{0, -1, p[1], 0}
	default:
		dy, err := lineArithmetic(ArithmeticSubtract, p[1], p[3])
		if err != nil {
			return Value{}, sqlstate.HardInputError(err)
		}
		dx, err := lineArithmetic(ArithmeticSubtract, p[0], p[2])
		if err != nil {
			return Value{}, sqlstate.HardInputError(err)
		}
		m, err := lineArithmetic(ArithmeticDivide, dy, dx)
		if err != nil {
			return Value{}, sqlstate.HardInputError(err)
		}
		if math.IsInf(m, 0) {
			c = [4]float64{-1, 0, p[0], 0}
		} else if m == 0 {
			c = [4]float64{0, -1, p[1], 0}
		} else {
			product, err := lineArithmetic(ArithmeticMultiply, m, p[0])
			if err != nil {
				return Value{}, sqlstate.HardInputError(err)
			}
			intercept, err := lineArithmetic(ArithmeticSubtract, p[1], product)
			if err != nil {
				return Value{}, sqlstate.HardInputError(err)
			}
			if intercept == 0 {
				intercept = 0
			}
			c = [4]float64{m, -1, intercept, 0}
		}
	}
	return Value{typ: d, geometry: &geometryPayload{coordinates: c}}, nil
}

func lineEqual(a, b Value) (bool, error) {
	if a.geometry == nil || b.geometry == nil {
		return false, fmt.Errorf("missing LINE payload")
	}
	x, y := a.geometry.coordinates, b.geometry.coordinates
	for i := range 3 {
		if math.IsNaN(x[i]) || math.IsNaN(y[i]) {
			return compareFloats(x[0], y[0]) == 0 && compareFloats(x[1], y[1]) == 0 && compareFloats(x[2], y[2]) == 0, nil
		}
	}
	ratio := 1.0
	for i := range 3 {
		if !boxFuzzyEqual(y[i], 0) {
			var err error
			ratio, err = lineArithmetic(ArithmeticDivide, x[i], y[i])
			if err != nil {
				return false, err
			}
			break
		}
	}
	for i := range 3 {
		product, err := lineArithmetic(ArithmeticMultiply, ratio, y[i])
		if err != nil {
			return false, err
		}
		if !boxFuzzyEqual(x[i], product) {
			return false, nil
		}
	}
	return true, nil
}
