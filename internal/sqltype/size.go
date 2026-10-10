package sqltype

import (
	"context"
	"math/big"
	"strconv"
	"stuntgres/internal/sqlstate"
)

var sizeUnits = [...]string{"bytes", "kB", "MB", "GB", "TB", "PB"}

func sizePrettyInt(n int64) string {
	for i, unit := range sizeUnits {
		magnitude := uint64(n)
		if n < 0 {
			magnitude = 0 - magnitude
		}
		limit := uint64(20479)
		if i == 0 {
			limit = 10240
		}
		if magnitude < limit || i == len(sizeUnits)-1 {
			if i != 0 {
				if n < 0 {
					n = (n - 1) / 2
				} else {
					n = (n + 1) / 2
				}
			}
			return strconv.FormatInt(n, 10) + " " + unit
		}
		divisor := int64(1024)
		if i == 0 {
			divisor = 512
		}
		n /= divisor
	}
	panic("unreachable size unit")
}

func sizePrettyNumeric(ctx context.Context, n Value) (string, error) {
	for i, unit := range sizeUnits {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		magnitude := n
		magnitude.negative = false
		if magnitude.special == numericNegativeInfinity {
			magnitude.special = numericInfinity
		}
		limit := Value{typ: n.typ, text: "20479"}
		if i == 0 {
			limit.text = "10240"
		}
		if compareNumeric(magnitude, limit) < 0 || i == len(sizeUnits)-1 {
			if i != 0 && n.special == numericFinite {
				// Transitions have already truncated to scale zero. Increase the
				// unsigned integer magnitude once, then truncate division by two.
				var coefficient, power, one big.Int
				if n.text != "" {
					coefficient.SetString(n.text, 10)
					if n.exponent > 0 {
						power.Exp(big.NewInt(10), big.NewInt(n.exponent), nil)
						coefficient.Mul(&coefficient, &power)
					}
				}
				one.SetInt64(1)
				coefficient.Add(&coefficient, &one)
				if err := ctx.Err(); err != nil {
					return "", err
				}
				coefficient.Rsh(&coefficient, 1)
				if err := ctx.Err(); err != nil {
					return "", err
				}
				n = normalizeNumeric(Value{typ: n.typ, text: coefficient.String(), negative: n.negative})
			}
			text := numericOutput(n) + " " + unit
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return text, nil
		}
		divisor := "1024"
		if i == 0 {
			divisor = "512"
		}
		var err error
		n, err = numericDivideTrunc(ctx, n, Value{typ: n.typ, text: divisor})
		if err != nil {
			return "", err
		}
	}
	panic("unreachable size unit")
}

// The size scanner is deliberately narrower than numeric input: no radix,
// separators or special values; strtol-style exponent whitespace is consumed
// and then rejected by the numeric codec with the extracted original spelling.
func sizeBytes(ctx context.Context, result Usage, original string) (Value, error) {
	invalid := func(unit string) error {
		d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid size: "` + original + `"`}
		if unit != "" {
			d.Detail = `Invalid size unit: "` + unit + `".`
			d.Hint = `Valid units are "bytes", "B", "kB", "MB", "GB", "TB", and "PB".`
		}
		return d
	}
	i, err := sizeSkipSpace(ctx, original, 0)
	if err != nil {
		return Value{}, err
	}
	start := i
	if i < len(original) && (original[i] == '+' || original[i] == '-') {
		i++
	}
	digits := i
	i, err = sizeScanDigits(ctx, original, i)
	if err != nil {
		return Value{}, err
	}
	count := i - digits
	if i < len(original) && original[i] == '.' {
		i++
		digits = i
		i, err = sizeScanDigits(ctx, original, i)
		if err != nil {
			return Value{}, err
		}
		count += i - digits
	}
	if count == 0 {
		return Value{}, invalid("")
	}
	if i < len(original) && (original[i] == 'e' || original[i] == 'E') {
		exponent, err := sizeSkipSpace(ctx, original, i+1)
		if err != nil {
			return Value{}, err
		}
		if exponent < len(original) && (original[exponent] == '+' || original[exponent] == '-') {
			exponent++
		}
		end, err := sizeScanDigits(ctx, original, exponent)
		if err != nil {
			return Value{}, err
		}
		if end > exponent {
			i = end
		}
	}
	n, err := parseNumeric(original[start:i])
	if err != nil {
		return Value{}, err
	}
	if err := validateNumericFormat(n); err != nil {
		return Value{}, err
	}
	i, err = sizeSkipSpace(ctx, original, i)
	if err != nil {
		return Value{}, err
	}
	end := len(original)
	for end > i && space(original[end-1]) {
		end--
		if end%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
	}
	unit := original[i:end]
	bits := uint(0)
	if unit != "" && !sizeUnitEqual(unit, "B") {
		matched := false
		for index, name := range sizeUnits {
			if sizeUnitEqual(unit, name) {
				bits, matched = uint(index*10), true
				break
			}
		}
		if !matched {
			return Value{}, invalid(unit)
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if bits != 0 && n.text != "" {
		var coefficient big.Int
		coefficient.SetString(n.text, 10)
		coefficient.Lsh(&coefficient, bits)
		n.text = coefficient.String()
		n = normalizeNumeric(n)
		if err := validateNumericFormat(n); err != nil {
			return Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	value, err := numericCoercion(ctx, n, result)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return value, nil
}

func sizeScanDigits(ctx context.Context, s string, i int) (int, error) {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
	}
	return i, nil
}

func sizeSkipSpace(ctx context.Context, s string, i int) (int, error) {
	for i < len(s) && space(s[i]) {
		i++
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
	}
	return i, nil
}

func sizeUnitEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		c, d := a[i], b[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if d >= 'A' && d <= 'Z' {
			d += 'a' - 'A'
		}
		if c != d {
			return false
		}
	}
	return true
}
