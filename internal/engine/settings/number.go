package settings

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

func realText(n float64) string {
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	return strconv.FormatFloat(n, 'g', 6, 64)
}

// ParseNumber scans a numeric prefix and returns its trimmed unit suffix.
// Integer input first uses strtol(base 0), falling back to strtod for
// fractions/exponents or overflow.
func ParseNumber(input string, integer bool) (float64, string, bool) {
	s := strings.TrimLeft(input, " \t\n\r\v\f")
	if s == "" {
		return 0, "", false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
		if i == len(s) {
			return 0, "", false
		}
	}
	if integer {
		// strtol's actual stopping point matters: 08.5 and 0x1p0 do not
		// trigger its decimal-point/e fallback, whereas 010.5 does.
		base, start := 10, i
		if s[i] == '0' {
			base = 8
			if i+2 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') && digitValue(s[i+2]) >= 0 {
				base = 16
				start = i + 2
			}
		}
		end := start
		for end < len(s) {
			digit := digitValue(s[end])
			if digit < 0 || digit >= base {
				break
			}
			end++
		}
		if end == start {
			if input[0] != '.' && input[0] != 'e' && input[0] != 'E' {
				return 0, "", false
			}
		} else {
			n, err := strconv.ParseInt(s[:end], 0, 64)
			overflow := false
			if err != nil {
				ne, yes := err.(*strconv.NumError)
				if !yes || ne.Err != strconv.ErrRange {
					return 0, "", false
				}
				overflow = true
			}
			if !overflow && (end == len(s) || s[end] != '.' && s[end] != 'e' && s[end] != 'E') {
				return float64(n), strings.Trim(s[end:], " \t\n\r\v\f"), true
			}
		}
	}
	if !integer && (s[i] == 'i' || s[i] == 'I') {
		for _, word := range []string{"infinity", "inf"} {
			if len(s)-i >= len(word) && strings.EqualFold(s[i:i+len(word)], word) {
				sign := 1
				if s[0] == '-' {
					sign = -1
				}
				return math.Inf(sign), strings.Trim(s[i+len(word):], " \t\n\r\v\f"), true
			}
		}
	}
	start := i
	numberStart := i
	hex := i+2 <= len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X')
	if hex {
		i += 2
		start = i
	}
	digit := func(c byte) bool {
		return c >= '0' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
	}
	for i < len(s) && digit(s[i]) {
		i++
	}
	digits := i - start
	if i < len(s) && s[i] == '.' {
		i++
		start = i
		for i < len(s) && digit(s[i]) {
			i++
		}
		digits += i - start
	}
	if digits == 0 {
		if hex {
			return 0, strings.Trim(s[numberStart+1:], " \t\n\r\v\f"), true
		}
		return 0, "", false
	}
	mantissaEnd := i
	if i < len(s) && (!hex && (s[i] == 'e' || s[i] == 'E') || hex && (s[i] == 'p' || s[i] == 'P')) {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			i = mantissaEnd
		}
	}
	token := s[:i]
	suffix := strings.Trim(s[i:], " \t\n\r\v\f")
	if hex && !strings.ContainsAny(token, "pP") {
		token += "p0"
	}
	n, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, "", false
	}
	if n != 0 && math.Abs(n) < 0x1p-1022 {
		exact := false
		if hex {
			exact = exactHexSubnormal(s[:mantissaEnd], token[mantissaEnd+1:])
		} else {
			// Only the exceptional decimal-subnormal path needs arbitrary
			// precision: strtod reports ERANGE if conversion discarded bits.
			var decimal, binary big.Rat
			_, valid := decimal.SetString(token)
			exact = valid && decimal.Cmp(binary.SetFloat64(n)) == 0
		}
		if !exact {
			return 0, "", false
		}
	}
	// Go does not report all strtod underflows. Reject a nonzero mantissa
	// rounded to zero, rather than silently accepting a different setting.
	if n == 0 {
		for _, c := range s[:mantissaEnd] {
			if c >= '1' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return 0, "", false
			}
		}
	}
	return n, suffix, err == nil && !math.IsNaN(n)
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// strtod accepts exact hexadecimal subnormals, but reports ERANGE when a
// nonzero bit below the 2^-1074 grid was lost to rounding.
func exactHexSubnormal(mantissa, exponent string) bool {
	exp, err := strconv.ParseInt(exponent, 10, 64)
	if err != nil {
		return false
	}
	if mantissa[0] == '+' || mantissa[0] == '-' {
		mantissa = mantissa[1:]
	}
	mantissa = mantissa[2:]
	integerDigits := strings.IndexByte(mantissa, '.')
	if integerDigits < 0 {
		integerDigits = len(mantissa)
	}
	position, last, nibble := 0, -1, 0
	for i := range len(mantissa) {
		if mantissa[i] == '.' {
			continue
		}
		digit := digitValue(mantissa[i])
		if digit > 0 {
			last = position
			nibble = digit
		}
		position++
	}
	if last < 0 {
		return true
	}
	exp += 4 * int64(integerDigits-last-1)
	for nibble&1 == 0 {
		exp++
		nibble >>= 1
	}
	return exp >= -1074
}
