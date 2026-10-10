package sqltype

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"
)

// numericFormatInput follows template actions rather than requiring complete
// source consumption. Nondata actions skip codepoints, never arbitrary bytes.
func numericFormatInput(ctx context.Context, d *Descriptor, source string, t numericTemplate, env InputEnvironment) (Value, error) {
	if t.science {
		return Value{}, failure("0A000", `"EEEE" not supported for input`)
	}
	numeric, monetary := env.NumericLocale.policy(), env.MonetaryLocale.policy()
	decimalMark := "."
	if t.localeDecimal {
		decimalMark = numeric.decimal
	}
	readSign := func(at int) (byte, int) {
		if strings.HasPrefix(source[at:], numeric.negative) {
			return '-', len(numeric.negative)
		}
		if strings.HasPrefix(source[at:], numeric.positive) {
			return '+', len(numeric.positive)
		}
		return 0, 0
	}
	result := make([]byte, 1, 1+min(len(source), len(t.nodes)))
	result[0] = ' '
	cursor, pre, post := 0, 0, 0
	decimal := false
	eat := func(n int) {
		for ; n > 0 && cursor < len(source); n-- {
			if strings.ContainsRune("0123456789.,+-", rune(source[cursor])) {
				break
			}
			_, w := utf8.DecodeRuneInString(source[cursor:])
			cursor += w
		}
	}
	for i, node := range t.nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		if cursor >= len(source) {
			break
		}
		key := numericFormatKind(node.key)
		switch key {
		case "9", "0", "D", ".":
			if source[cursor] == ' ' {
				cursor++
			}
			if cursor >= len(source) {
				continue
			}
			if result[0] == ' ' && (key == "9" || key == "0") && pre+post == 0 {
				c := source[cursor]
				if t.sign == 1 {
					if sign, width := readSign(cursor); width > 0 {
						result[0] = sign
						cursor += width
					}
				} else if c == '-' || (t.bracket && c == '<') {
					result[0] = '-'
					cursor++
				} else if c == '+' {
					result[0] = '+'
					cursor++
				}
			}
			if cursor >= len(source) {
				continue
			}
			read := false
			c := source[cursor]
			if c >= '0' && c <= '9' {
				if decimal && post == t.post {
					cursor++
					continue
				}
				result = append(result, c)
				if decimal {
					post++
				} else {
					pre++
				}
				read = true
			} else if t.decimal && !decimal && strings.HasPrefix(source[cursor:], decimalMark) {
				result = append(result, '.')
				decimal = true
				read = true
				cursor += len(decimalMark) - 1
			}
			if result[0] == ' ' && pre+post > 0 {
				if t.sign != 0 && read && cursor+1 < len(source) && (source[cursor+1] < '0' || source[cursor+1] > '9') {
					if sign, width := readSign(cursor + 1); width > 0 {
						result[0] = sign
						cursor += width
					}
				} else if !read && t.sign == 0 && (t.plus || t.minus) && (c == '-' || c == '+') {
					result[0] = c
				}
			}
			cursor++
		case ",", "G":
			grouping := ","
			if key == "G" {
				grouping = numeric.grouping
			}
			if strings.HasPrefix(source[cursor:], grouping) {
				cursor += len(grouping)
			}
		case "L":
			if strings.HasPrefix(source[cursor:], monetary.currency) {
				cursor += len(monetary.currency)
			} else {
				eat(utf8.RuneCountInString(monetary.currency))
			}
		case "RN":
			n, consumed, err := numericReadRoman(ctx, source[cursor:])
			if err != nil {
				return Value{}, err
			}
			cursor += consumed
			result = strconv.AppendInt(result, int64(n), 10)
			t.pre = len(strconv.Itoa(n))
			t.post = 0
		case "TH":
			if !t.roman && !t.decimal {
				eat(2)
			}
		case "MI", "PL", "SG":
			c := source[cursor]
			if (c == '-' && (key == "MI" || key == "SG")) || (c == '+' && (key == "PL" || key == "SG")) {
				result[0] = c
				cursor++
			} else {
				eat(1)
			}
		default:
			if node.literal != "" {
				_, w := utf8.DecodeRuneInString(source[cursor:])
				cursor += w
			}
		}
	}
	if result[len(result)-1] == '.' {
		result = result[:len(result)-1]
	}
	// Mirror the release reader's private int32 typmod, not public declarations
	// (which limit precision to1000). Encoding and signed-scale decoding also
	// govern very long masks, before numeric payload bounds are enforced.
	v, err := d.numericInput(string(result), numericFormatTypmod(t.pre+t.multi+post, post))
	if err != nil {
		return Value{}, err
	}
	if t.shift {
		factorScale := min(16+t.multi, 1000)
		factor := roundNumeric(Value{typ: d, text: "1", exponent: -int64(t.multi)}, int64(factorScale))
		scale := min(int64(16383), v.scale+int64(factorScale))
		if factor.text == "" {
			v = Value{typ: d, scale: scale}
		} else {
			if v.text != "" {
				v.exponent += factor.exponent
			}
			v.scale = scale
			v = roundNumeric(v, scale)
		}
	}
	return v, nil
}
func numericFormatTypmod(precision, scale int) int32 {
	typmod := int32(uint32(precision)<<16|uint32(scale)) + 4
	if typmod < 4 {
		return -1
	}
	return typmod
}
func numericReadRoman(ctx context.Context, s string) (int, int, error) {
	cursor := 0
	for cursor < len(s) && datetimeSpace(s[cursor]) {
		if cursor%64 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		cursor++
	}
	var chars [15]byte
	var values [15]int
	length := 0
	for length < 15 && cursor < len(s) {
		c := s[cursor]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		n := numericRomanValue(c)
		if n == 0 {
			break
		}
		chars[length], values[length] = c, n
		length++
		cursor++
	}
	invalid := func() (int, int, error) { return 0, 0, failure("22P02", "invalid Roman numeral") }
	if length == 0 {
		return invalid()
	}
	result, repeat, vcount, lcount, dcount, lastSubtract := 0, 1, 0, 0, 0, 0
	count := func(c byte) {
		switch c {
		case 'V':
			vcount++
		case 'L':
			lcount++
		case 'D':
			dcount++
		}
	}
	forbidden := func(n int) bool { return vcount > 0 && n >= 5 || lcount > 0 && n >= 50 || dcount > 0 && n >= 500 }
	for i := 0; i < length; i++ {
		c, n := chars[i], values[i]
		if lastSubtract > 0 && n >= lastSubtract || forbidden(n) {
			return invalid()
		}
		count(c)
		if i+1 < length {
			next := values[i+1]
			if n < next {
				valid := c == 'I' && (next == 5 || next == 10) || c == 'X' && (next == 50 || next == 100) || c == 'C' && (next == 500 || next == 1000)
				if !valid || repeat > 1 || forbidden(next) {
					return invalid()
				}
				count(chars[i+1])
				i++
				repeat = 1
				lastSubtract = n
				result += next - n
			} else {
				if c == chars[i+1] {
					repeat++
					if repeat > 3 {
						return invalid()
					}
				} else {
					repeat = 1
				}
				result += n
			}
		} else {
			result += n
		}
	}
	return result, cursor, nil
}
func numericRomanValue(c byte) int {
	switch c {
	case 'I':
		return 1
	case 'V':
		return 5
	case 'X':
		return 10
	case 'L':
		return 50
	case 'C':
		return 100
	case 'D':
		return 500
	case 'M':
		return 1000
	}
	return 0
}
