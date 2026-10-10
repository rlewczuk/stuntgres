package sqltype

import (
	"fmt"
	"math"
	"strconv"
)

// MoneyValue stores signed minor units, never a locale or floating amount.
func MoneyValue(d *Descriptor, units int64) (Value, error) {
	if d == nil || !d.Concrete() || d.id != Money || d.IsDomain() {
		return Value{}, fmt.Errorf("invalid money descriptor")
	}
	return Value{typ: d, integer: units}, nil
}

func (v Value) MoneyUnits() (int64, error) {
	if v.typ == nil || v.typ.BaseType().id != Money || v.null {
		return 0, fmt.Errorf("invalid money value")
	}
	return v.integer, nil
}

func moneySpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func (d *Descriptor) moneyInput(original string, locale Locale) (Value, error) {
	precision := locale.policy().moneyPrecision
	i := 0
	for i < len(original) && moneySpace(original[i]) {
		i++
	}
	negative := false
	if i < len(original) && original[i] == '$' {
		i++
	}
	for i < len(original) && moneySpace(original[i]) {
		i++
	}
	if i < len(original) {
		switch original[i] {
		case '-', '(':
			negative = true
			i++
		case '+':
			i++
		}
	}
	for i < len(original) && moneySpace(original[i]) {
		i++
	}
	if i < len(original) && original[i] == '$' {
		i++
	}
	for i < len(original) && moneySpace(original[i]) {
		i++
	}
	// Accumulate negatively to admit the extra negative endpoint. Signs can
	// appear at either end, so positive-range checking must happen last.
	var units int64
	fraction, decimal := 0, false
	overflow := func() (Value, error) {
		return Value{}, failure("22003", "value \""+original+"\" is out of range for type money")
	}
	for i < len(original) {
		c := original[i]
		if c >= '0' && c <= '9' && (!decimal || fraction < precision) {
			digit := int64(c - '0')
			if units < (math.MinInt64+digit)/10 {
				return overflow()
			}
			units = units*10 - digit
			if decimal {
				fraction++
			}
			i++
		} else if c == '.' && !decimal {
			decimal = true
			i++
		} else if c == ',' {
			i++
		} else {
			break
		}
	}
	if i < len(original) && original[i] >= '5' && original[i] <= '9' {
		if units == math.MinInt64 {
			return overflow()
		}
		units--
	}
	for fraction < precision {
		if units < math.MinInt64/10 {
			return overflow()
		}
		units *= 10
		fraction++
	}
	// Only contiguous excess digits are discarded; grouping after a discarded
	// digit belongs to the invalid suffix, just as in REL_18_0 cash_in.
	for i < len(original) && original[i] >= '0' && original[i] <= '9' {
		i++
	}
	for i < len(original) {
		switch original[i] {
		case '-':
			negative = true
		case '+', ')', '$':
		default:
			if !moneySpace(original[i]) {
				return Value{}, failure("22P02", "invalid input syntax for type money: \""+original+"\"")
			}
		}
		i++
	}
	if !negative {
		if units == math.MinInt64 {
			return overflow()
		}
		units = -units
	}
	return MoneyValue(d, units)
}

func moneyOutput(units int64, locale Locale) string {
	policy := locale.policy()
	magnitude := uint64(units)
	if units < 0 {
		magnitude = uint64(-(units + 1)) + 1
	}
	var digits [20]byte
	raw := strconv.AppendUint(digits[:0], magnitude, 10)
	var output [40]byte
	out := output[:0]
	if units < 0 {
		out = append(out, '-')
	}
	out = append(out, policy.moneyCurrency...)
	whole := len(raw) - policy.moneyPrecision
	if whole <= 0 {
		out = append(out, '0')
	} else {
		for i := range whole {
			if i > 0 && (whole-i)%3 == 0 {
				out = append(out, policy.grouping...)
			}
			out = append(out, raw[i])
		}
	}
	out = append(out, policy.decimal...)
	for i := whole; i < len(raw); i++ {
		if i < 0 {
			out = append(out, '0')
		} else {
			out = append(out, raw[i])
		}
	}
	return string(out)
}

func moneyCoercion(v Value, target Usage, locale Locale) (Value, error) {
	precision := int64(locale.policy().moneyPrecision)
	if target.Type.id == Numeric {
		text := strconv.FormatInt(v.integer, 10)
		negative := v.integer < 0
		if negative {
			text = text[1:]
		}
		return sizeNumeric(normalizeNumeric(Value{typ: target.Type, text: text, negative: negative, exponent: -precision, scale: precision}), target.Typmod)
	}
	if v.typ.id == Numeric {
		v.exponent += precision
		units, err := numericInteger(v, Int8)
		if err != nil {
			return Value{}, err
		}
		return MoneyValue(target.Type, units)
	}
	scale := int64(1)
	for range precision {
		scale *= 10
	}
	if v.integer > math.MaxInt64/scale || v.integer < math.MinInt64/scale {
		return Value{}, failure("22003", "bigint out of range")
	}
	return MoneyValue(target.Type, v.integer*scale)
}

func (p Arithmetic) moneyBinary(a, b Value) (Value, error) {
	if p.right.id == Money && p.operator == ArithmeticDivide {
		if b.integer == 0 {
			return Value{}, failure("22012", "division by zero")
		}
		return p.floatingBinary(float64(a.integer), float64(b.integer))
	}
	if p.left.id != Money {
		a, b = b, a
	}
	if isFloat(b.typ.id) {
		// floatingBinary's FLOAT8 branch checks the intermediate before cash's
		// nearest-even rounding. The temporary value never leaves this provider.
		intermediate, err := p.floatingBinary(float64(a.integer), b.floating)
		if err != nil {
			return Value{}, err
		}
		n := math.RoundToEven(intermediate.floating)
		if math.IsNaN(n) || n < -9223372036854775808.0 || n >= 9223372036854775808.0 {
			return Value{}, failure("22003", "money out of range")
		}
		return MoneyValue(p.result, int64(n))
	}
	x, y := a.integer, b.integer
	var n int64
	overflow := false
	switch p.operator {
	case ArithmeticAdd:
		overflow = y > 0 && x > math.MaxInt64-y || y < 0 && x < math.MinInt64-y
		if !overflow {
			n = x + y
		}
	case ArithmeticSubtract:
		overflow = y < 0 && x > math.MaxInt64+y || y > 0 && x < math.MinInt64+y
		if !overflow {
			n = x - y
		}
	case ArithmeticMultiply:
		if x != 0 && y != 0 {
			if x == -1 {
				overflow = y == math.MinInt64
			} else if y == -1 {
				overflow = x == math.MinInt64
			} else {
				n = x * y
				overflow = n/y != x
			}
		}
		if !overflow {
			n = x * y
		}
	case ArithmeticDivide:
		if y == 0 {
			return Value{}, failure("22012", "division by zero")
		}
		// Deliberate safe native deviation from REL_18_0 cash_div_int64.
		overflow = x == math.MinInt64 && y == -1
		if !overflow {
			n = x / y
		}
	}
	if overflow {
		return Value{}, failure("22003", "money out of range")
	}
	return MoneyValue(p.result, n)
}
