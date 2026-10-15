package sqltype

import "math"

func logarithmRootPower(name string, x, y float64) (float64, error) {
	switch name {
	case "power":
		return floatingPower(x, y)
	case "ln", "log", "log10":
		if x == 0 {
			return 0, failure("2201E", "cannot take logarithm of zero")
		}
		if x < 0 {
			return 0, failure("2201E", "cannot take logarithm of a negative number")
		}
		if name == "ln" {
			return math.Log(x), nil
		}
		return math.Log10(x), nil
	case "sqrt":
		if x < 0 {
			return 0, failure("2201F", "cannot take square root of a negative number")
		}
		return math.Sqrt(x), nil
	case "lgamma":
		if math.IsNaN(x) {
			return x, nil
		}
		if math.IsInf(x, 0) {
			return math.Inf(1), nil
		}
		if x <= 0 && math.Trunc(x) == x {
			return 0, failure("22003", "value out of range: overflow")
		}
		n, _ := math.Lgamma(x)
		if math.IsInf(n, 0) {
			return 0, failure("22003", "value out of range: overflow")
		}
		return n, nil
	}
	panic("invalid logarithm/root operation")
}
