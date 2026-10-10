package sqltype

import (
	"context"
	"math"
	"strconv"
	"strings"
)

func numericFormatOutput(ctx context.Context, v Value, t numericTemplate, env OutputEnvironment) (string, error) {
	if len(t.nodes) == 0 {
		return "", nil
	}
	number := ""
	negative := false
	floating := v.typ.id == Float4 || v.typ.id == Float8
	if t.roman {
		n := 0
		if floating {
			f := math.RoundToEven(v.floating)
			if f >= 1 && f <= 3999 {
				n = int(f)
			}
		} else {
			x := roundNumeric(exactOperand(v), 0)
			if x.special == numericFinite && !x.negative && len(x.text)+int(x.exponent) <= 4 {
				n, _ = strconv.Atoi(numericOutput(x))
			}
		}
		number = numericRoman(n)
	} else if t.science {
		special := v.special != numericFinite
		if floating {
			special = math.IsNaN(v.floating) || math.IsInf(v.floating, 0)
		}
		if special {
			return " " + strings.Repeat("#", t.pre) + "." + strings.Repeat("#", t.post+4), nil
		}
		if floating || v.typ.id == Int4 {
			f := v.floating
			if v.typ.id == Int4 {
				f = float64(v.integer)
			}
			s := strconv.FormatFloat(f, 'e', t.post, 64)
			if !strings.HasPrefix(s, "-") {
				s = " " + s
			}
			return s, nil
		}
		x := exactOperand(v)
		exponent := int64(0)
		if x.text != "" {
			exponent = int64(len(x.text)) + x.exponent - 1
			x.exponent -= exponent
		}
		x = roundNumeric(x, int64(t.post))
		s := numericOutput(x)
		sign := "+"
		if exponent < 0 {
			sign = "-"
			exponent = -exponent
		}
		e := strconv.FormatInt(exponent, 10)
		if len(e) < 2 {
			e = "0" + e
		}
		if !x.negative {
			s = " " + s
		}
		return s + "e" + sign + e, nil
	} else if floating {
		f := v.floating
		if t.shift {
			multiplier := math.Pow10(t.multi)
			if v.typ.id == Float4 {
				multiplier = float64(float32(multiplier))
				f = float64(float32(f * multiplier))
			} else {
				f *= multiplier
			}
			t.pre += t.multi
		}
		precision := 15
		if v.typ.id == Float4 {
			precision = 6
		}
		rounded := numericFloatFixed(math.Abs(f), 0)
		t.post = min(t.post, max(0, precision-len(rounded)))
		number = numericFloatFixed(f, t.post)
		negative = strings.HasPrefix(number, "-")
		number = strings.TrimPrefix(number, "-")
	} else {
		x := exactOperand(v)
		if t.shift {
			if v.typ.id == Int8 {
				// The selected INT8 wrapper converts pow(10,n) to int8 and invokes
				// checked int8 multiplication. This is not a general arithmetic route.
				if t.multi > 18 {
					return "", failure("22003", "bigint out of range")
				}
				factor := int64(1)
				for range t.multi {
					factor *= 10
				}
				if v.integer > math.MaxInt64/factor || v.integer < math.MinInt64/factor {
					return "", failure("22003", "bigint out of range")
				}
				v.integer *= factor
				x = exactOperand(v)
			} else if v.typ.id == Int4 {
				// Pin the release's machine conversion/multiply result explicitly:
				// out-of-range positive pow converts to INT32_MIN, then multiplication
				// retains the low32 bits. Go's unsigned arithmetic is defined.
				factor := uint32(1)
				if t.multi >= 10 {
					factor = uint32(1) << 31
				} else {
					for range t.multi {
						factor *= 10
					}
				}
				v.integer = int64(int32(uint32(v.integer) * factor))
				x = exactOperand(v)
			} else {
				if t.multi >= 131072 || x.text != "" && int64(len(x.text))+x.exponent+int64(t.multi) > 131072 {
					return "", failure("22003", "value overflows numeric format")
				}
				if x.text != "" {
					x.exponent += int64(t.multi)
				}
			}
			t.pre += t.multi
		}
		x = roundNumeric(x, int64(t.post))
		number = numericOutput(x)
		negative = x.negative || x.special == numericNegativeInfinity
		number = strings.TrimPrefix(number, "-")
	}
	spaces := 0
	if !t.roman {
		integer := len(number)
		if dot := strings.IndexByte(number, '.'); dot >= 0 {
			integer = dot
		}
		if integer < t.pre {
			spaces = t.pre - integer
		} else if integer > t.pre {
			number = strings.Repeat("#", t.pre) + "." + strings.Repeat("#", t.post)
		}
	}
	return numericRender(ctx, number, negative, spaces, t, env)
}
func numericFloatFixed(f float64, precision int) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'f', precision, 64)
}
func numericRender(ctx context.Context, number string, negative bool, spaces int, t numericTemplate, env OutputEnvironment) (string, error) {
	if t.zeroStart > 0 {
		t.zeroStart--
	}
	numeric := env.NumericLocale.policy()
	monetary := env.MonetaryLocale.policy()
	sign := numeric.positive
	if negative {
		sign = numeric.negative
	}
	signWritten := false
	if t.plus || t.minus {
		signWritten = t.minus
	} else {
		if !negative && t.fm {
			t.bracket = false
		}
		signWritten = !negative && t.fm && t.sign == 0
		if t.sign == 1 && t.pre == t.signPre {
			t.sign = 2
		}
	}
	count := t.post + t.pre - 1
	last := -1
	if t.fm && t.decimal {
		if dot := strings.IndexByte(number, '.'); dot >= 0 {
			last = len(number) - 1
			for last > dot && number[last] == '0' {
				last--
			}
			if t.zeroEnd > spaces {
				last = max(last, min(len(number)-1, t.zeroEnd-spaces))
			}
		}
	}
	if !signWritten && spaces == 0 {
		count++
	}
	var b strings.Builder
	b.Grow(len(number) + spaces + len(t.nodes) + 2)
	cursor, current := 0, 0
	inside := false
	for i, node := range t.nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		key := numericFormatKind(node.key)
		switch key {
		case "9", "0", "D", ".":
			if t.roman {
				continue
			}
			inside = false
			predecimal := !t.zero && cursor == 0 && len(number) > 0 && number[0] == '0' && t.post != 0
			if !signWritten && (current >= spaces || (t.zero && t.zeroStart == current)) && (!predecimal || (last >= 0 && number[last] == '.')) {
				if t.sign != 0 {
					if t.sign == 1 {
						b.WriteString(sign)
						signWritten = true
					}
				} else if t.bracket {
					if negative {
						b.WriteByte('<')
					} else {
						b.WriteByte(' ')
					}
					signWritten = true
				} else {
					if negative {
						b.WriteByte('-')
					} else if !t.fm {
						b.WriteByte(' ')
					}
					signWritten = true
				}
			}
			if current < spaces && (!t.zero || t.zeroStart > current) {
				if !t.fm {
					b.WriteByte(' ')
				}
			} else if t.zero && current < spaces && t.zeroStart <= current {
				b.WriteByte('0')
				inside = true
			} else {
				if cursor < len(number) {
					c := number[cursor]
					if c == '.' {
						if key == "D" {
							b.WriteString(numeric.decimal)
						} else {
							b.WriteByte('.')
						}
					} else if last >= 0 && cursor > last && key != "0" {
					} else if predecimal {
						if !t.fm {
							b.WriteByte(' ')
						} else if last >= 0 && number[last] == '.' {
							b.WriteByte('0')
						}
					} else {
						b.WriteByte(c)
						inside = true
					}
					cursor++
				}
			}
			end := count
			if spaces > 0 {
				end++
			}
			if t.decimal {
				end++
			}
			if last >= 0 && last == cursor {
				end = current
			}
			if current+1 == end {
				if signWritten && t.bracket {
					if negative {
						b.WriteByte('>')
					} else {
						b.WriteByte(' ')
					}
				} else if t.sign == 2 {
					b.WriteString(sign)
				}
			}
			current++
		case ",", "G":
			if inside {
				if key == "G" {
					b.WriteString(numeric.grouping)
				} else {
					b.WriteByte(',')
				}
			} else if !t.fm {
				b.WriteByte(' ')
			}
		case "L":
			b.WriteString(monetary.currency)
		case "RN":
			s := number[cursor:]
			if !t.fm {
				for range max(0, 15-len(s)) {
					b.WriteByte(' ')
				}
			}
			if node.key == "rn" {
				for i := range s {
					c := s[i]
					if c >= 'A' && c <= 'Z' {
						c += 'a' - 'A'
					}
					b.WriteByte(c)
				}
			} else {
				b.WriteString(s)
			}
		case "TH":
			if !t.roman && !negative && !t.decimal && len(number) > 0 && number[0] != '#' {
				s := numericOrdinal(number)
				if node.key == "th" {
					b.WriteByte(s[0] + 'a' - 'A')
					b.WriteByte(s[1] + 'a' - 'A')
				} else {
					b.WriteString(s)
				}
			}
		case "MI":
			if negative {
				b.WriteByte('-')
			} else if !t.fm {
				b.WriteByte(' ')
			}
		case "PL":
			if !negative {
				b.WriteByte('+')
			} else if !t.fm {
				b.WriteByte(' ')
			}
		case "SG":
			if negative {
				b.WriteByte('-')
			} else {
				b.WriteByte('+')
			}
		default:
			b.WriteString(node.literal)
		}
	}
	return b.String(), nil
}
func numericOrdinal(s string) string {
	n := 0
	start := max(0, len(s)-2)
	for i := start; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n = n*10 + int(s[i]-'0')
		}
	}
	if n >= 11 && n <= 13 {
		return "TH"
	}
	switch n % 10 {
	case 1:
		return "ST"
	case 2:
		return "ND"
	case 3:
		return "RD"
	}
	return "TH"
}

var numericRomanValues = [...]int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
var numericRomanDigits = [...]string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}

func numericRoman(n int) string {
	if n < 1 || n > 3999 {
		return "###############"
	}
	var b strings.Builder
	for i, value := range numericRomanValues {
		for n >= value {
			b.WriteString(numericRomanDigits[i])
			n -= value
		}
	}
	return b.String()
}
