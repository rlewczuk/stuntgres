package sqltype

import "math"

// hyperbolic evaluates only validated unary FLOAT8 MathPlans. Domains and poles
// follow PostgreSQL REL_18_0 float.c's hyperbolic section:
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/float.c
// Finite sinh/cosh overflow succeeds with infinity, unlike the exp SQL provider.
func hyperbolic(name string, x float64) (float64, error) {
	switch name {
	case "sinh", "cosh":
		if magnitude := math.Abs(x); magnitude > 20 {
			// exp(x)/2 remains representable beyond exp(x)'s finite range.
			// Split the exponent and halve before the final multiplication to
			// avoid premature infinity (notably at 710). Above 20 the omitted
			// exp(-x)/2 term is below binary64 rounding significance.
			w := math.Exp(magnitude / 2)
			result := (0.5 * w) * w
			if name == "sinh" {
				result = math.Copysign(result, x)
			}
			return result, nil
		}
		if name == "sinh" {
			return math.Sinh(x), nil
		}
		return math.Cosh(x), nil
	case "tanh":
		return math.Tanh(x), nil
	case "asinh":
		return math.Asinh(x), nil
	case "acosh":
		// NaN bypasses comparisons and propagates; +Infinity is valid.
		if x < 1 {
			return 0, failure("22003", "input is out of range")
		}
		return math.Acosh(x), nil
	case "atanh":
		if x < -1 || x > 1 {
			return 0, failure("22003", "input is out of range")
		}
		// Exactly +/-1 are successful signed infinities, not domain errors.
		return math.Atanh(x), nil
	}
	return 0, nil
}
