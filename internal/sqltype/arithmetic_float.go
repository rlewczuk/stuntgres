package sqltype

import "math"

func (p Arithmetic) floatingBinary(x, y float64) (Value, error) {
	if p.operator == ArithmeticPower {
		n, err := floatingPower(x, y)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: p.result, floating: n}, nil
	}
	n, err := checkedFloatingBinary(p.operator, p.result.id, x, y)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result, floating: n}, nil
}

// checkedFloatingBinary also serves fixed geometric datums; it never mints a
// temporary descriptor or value to perform a checked float8 calculation.
func checkedFloatingBinary(operator ArithmeticOperator, result ID, x, y float64) (float64, error) {
	var n float64
	if result == Float4 {
		a, b := float32(x), float32(y)
		switch operator {
		case ArithmeticAdd:
			n = float64(a + b)
		case ArithmeticSubtract:
			n = float64(a - b)
		case ArithmeticMultiply:
			n = float64(a * b)
		case ArithmeticDivide:
			n = float64(a / b)
		}
	} else {
		switch operator {
		case ArithmeticAdd:
			n = x + y
		case ArithmeticSubtract:
			n = x - y
		case ArithmeticMultiply:
			n = x * y
		case ArithmeticDivide:
			n = x / y
		}
	}
	if operator == ArithmeticDivide && y == 0 && !math.IsNaN(x) {
		return 0, failure("22012", "division by zero")
	}
	overflow := math.IsInf(n, 0) && !math.IsInf(x, 0)
	if operator != ArithmeticDivide {
		overflow = overflow && !math.IsInf(y, 0)
	}
	if overflow {
		return 0, failure("22003", "value out of range: overflow")
	}
	if n == 0 && x != 0 && (operator == ArithmeticMultiply && y != 0 || operator == ArithmeticDivide && !math.IsInf(y, 0)) {
		return 0, failure("22003", "value out of range: underflow")
	}
	return n, nil
}

func floatingPower(x, y float64) (float64, error) {
	if math.IsNaN(x) || math.IsNaN(y) {
		if y == 0 || x == 1 {
			return 1, nil
		}
		return math.NaN(), nil
	}
	if x == 0 && y < 0 {
		return 0, failure("2201F", "zero raised to a negative power is undefined")
	}
	if x < 0 && math.Floor(y) != y {
		return 0, failure("2201F", "a negative number raised to a non-integer power yields a complex result")
	}
	if math.IsInf(y, 0) {
		a := math.Abs(x)
		if a == 1 {
			return 1, nil
		}
		if (a > 1) == (y > 0) {
			return math.Inf(1), nil
		}
		return 0, nil
	}
	if math.IsInf(x, 0) {
		if y == 0 {
			return 1, nil
		}
		n := float64(0)
		if y > 0 {
			n = math.Inf(1)
		}
		if x < 0 && math.Floor(y/2) != y/2 {
			n = math.Copysign(n, -1)
		}
		return n, nil
	}
	n := math.Pow(x, y)
	if math.IsInf(n, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	if n == 0 && x != 0 {
		return 0, failure("22003", "value out of range: underflow")
	}
	return n, nil
}
