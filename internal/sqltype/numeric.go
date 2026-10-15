package sqltype

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

// Numeric uses immutable coefficient digits, a decimal exponent and a separate
// display scale. No mutable arbitrary-precision object is retained in a Value.
const (
	numericFinite uint8 = iota
	numericNegativeInfinity
	numericInfinity
	numericNaN
)

func exactNumber(id ID) bool              { return id == Numeric || id == Int2 || id == Int4 || id == Int8 }
func numericParts(p int32) (int32, int32) { return (p - 4) >> 16, (((p - 4) & 2047) ^ 1024) - 1024 }
func numericTypmodValid(p int32) bool {
	if p < 4 {
		return false
	}
	precision, scale := numericParts(p)
	return precision >= 1 && precision <= 1000 && scale >= -1000 && scale <= 1000 && p == ((precision<<16)|(scale&2047))+4
}
func (u Usage) DecimalSize() (int64, int64, bool) {
	u = u.BaseUsage()
	if u.Type != nil && u.Type.IsArray() {
		u.Type = u.Type.element
		return u.DecimalSize()
	}
	if u.Type == nil || u.Type.id != Numeric || u.Typmod < 0 {
		return 0, 0, false
	}
	p, s := numericParts(u.Typmod)
	return int64(p), int64(s), true
}
func normalizeNumeric(v Value) Value {
	v.text = strings.TrimLeft(v.text, "0")
	if v.text == "" {
		v.text = ""
		v.negative = false
		v.exponent = 0
		return v
	}
	end := len(v.text)
	for end > 0 && v.text[end-1] == '0' {
		end--
		v.exponent++
	}
	v.text = v.text[:end]
	return v
}
func (d *Descriptor) numericInput(original string, typmod int32) (Value, error) {
	v, err := parseNumeric(original)
	if err != nil {
		return Value{}, err
	}
	v.typ = d
	if typmod >= 0 {
		v, err = sizeNumeric(v, typmod)
		if err != nil {
			return Value{}, err
		}
	}
	if err := validateNumericFormat(v); err != nil {
		return Value{}, err
	}
	return v, nil
}

// parseNumeric returns private descriptor-neutral decimal scratch. Input codecs
// attach their catalog owner before sizing; exact providers never publish it.
func parseNumeric(original string) (Value, error) {
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type numeric: \"%s\"", original))
	}
	s := trim(original)
	v := Value{}
	switch {
	case strings.EqualFold(s, "NaN"):
		v.special = numericNaN
	case strings.EqualFold(s, "Infinity") || strings.EqualFold(s, "Inf") || strings.EqualFold(s, "+Infinity") || strings.EqualFold(s, "+Inf"):
		v.special = numericInfinity
	case strings.EqualFold(s, "-Infinity") || strings.EqualFold(s, "-Inf"):
		v.special = numericNegativeInfinity
	default:
		if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
			v.negative = s[0] == '-'
			s = s[1:]
		}
		if s == "" {
			return invalid()
		}
		base := uint64(10)
		if len(s) > 2 && s[0] == '0' {
			switch s[1] {
			case 'x', 'X':
				base = 16
			case 'o', 'O':
				base = 8
			case 'b', 'B':
				base = 2
			}
		}
		if base != 10 {
			digits := s[2:]
			if strings.HasPrefix(digits, "_") {
				digits = digits[1:]
			}
			if digits == "" {
				return invalid()
			}
			previous := false
			for i := range digits {
				if digits[i] == '_' {
					if !previous || i+1 == len(digits) {
						return invalid()
					}
					previous = false
					continue
				}
				if _, ok := digit(digits[i], base); !ok {
					return invalid()
				}
				previous = true
			}
			n, ok := new(big.Int).SetString(strings.ReplaceAll(digits, "_", ""), int(base))
			if !ok {
				return invalid()
			}
			v.text = n.String()
		} else {
			i := 0
			fraction := int64(0)
			total := 0
			scan := func(frac bool) bool {
				previous := false
				for i < len(s) {
					c := s[i]
					if c >= '0' && c <= '9' {
						total++
						if frac {
							fraction++
						}
						previous = true
						i++
						continue
					}
					if c == '_' {
						if !previous || i+1 == len(s) || s[i+1] < '0' || s[i+1] > '9' {
							return false
						}
						previous = false
						i++
						continue
					}
					break
				}
				return true
			}
			if !scan(false) {
				return invalid()
			}
			if i < len(s) && s[i] == '.' {
				i++
				if !scan(true) {
					return invalid()
				}
			}
			if total == 0 {
				return invalid()
			}
			coefficientEnd := i
			exponent := int64(0)
			if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
				i++
				negative := false
				if i < len(s) && (s[i] == '+' || s[i] == '-') {
					negative = s[i] == '-'
					i++
				}
				start := i
				previous := false
				overflow := false
				for i < len(s) {
					c := s[i]
					if c >= '0' && c <= '9' {
						if exponent > 107374182 || (exponent == 107374182 && c > '3') {
							overflow = true
						} else if !overflow {
							exponent = exponent*10 + int64(c-'0')
						}
						previous = true
						i++
						continue
					}
					if c == '_' {
						if !previous || i+1 == len(s) || s[i+1] < '0' || s[i+1] > '9' {
							return invalid()
						}
						previous = false
						i++
						continue
					}
					break
				}
				if i == start {
					return invalid()
				}
				if i != len(s) {
					return invalid()
				}
				if overflow {
					return Value{}, failure("22003", "value overflows numeric format")
				}
				if negative {
					exponent = -exponent
				}
			}
			if i != len(s) {
				return invalid()
			}
			v.text = numericCoefficient(s[:coefficientEnd], total)
			v.exponent = exponent - fraction
			v.scale = fraction - exponent
			if v.scale < 0 {
				v.scale = 0
			}
		}
		v = normalizeNumeric(v)
	}
	return v, nil
}

func validateNumericFormat(v Value) error {
	if v.special == numericFinite && (v.scale > 16383 || v.text != "" && int64(len(v.text))+v.exponent > 131072) {
		return failure("22003", "value overflows numeric format")
	}
	return nil
}
func numericCoefficient(s string, total int) string {
	if !strings.ContainsRune(s, '_') {
		dot := strings.IndexByte(s, '.')
		if dot < 0 {
			return s
		}
		if dot == 0 {
			return s[1:]
		}
		if dot == len(s)-1 {
			return s[:dot]
		}
	}
	var b strings.Builder
	b.Grow(total)
	for i := range s {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
func numericOutput(v Value) string {
	switch v.special {
	case numericNaN:
		return "NaN"
	case numericInfinity:
		return "Infinity"
	case numericNegativeInfinity:
		return "-Infinity"
	}
	if v.scale == 0 && !v.negative {
		if v.text == "" {
			return "0"
		}
		if v.exponent == 0 {
			return v.text
		}
	}
	var b strings.Builder
	// Background cannot fail; callers rendering request-owned output use their
	// own context and discard the private builder on cancellation.
	_ = appendNumericOutput(context.Background(), &b, v)
	return b.String()
}

func appendNumericOutput(ctx context.Context, b *strings.Builder, v Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch v.special {
	case numericNaN:
		b.WriteString("NaN")
		return nil
	case numericInfinity:
		b.WriteString("Infinity")
		return nil
	case numericNegativeInfinity:
		b.WriteString("-Infinity")
		return nil
	}
	point := int64(len(v.text)) + v.exponent
	integer := max(point, 1)
	length := integer
	if v.scale > 0 {
		length += 1 + v.scale
	}
	if v.negative {
		length++
	}
	b.Grow(int(length))
	if v.negative {
		b.WriteByte('-')
	}
	for i := range integer {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		index := i
		if point <= 0 {
			index = -1
		}
		if index >= 0 && index < int64(len(v.text)) {
			b.WriteByte(v.text[index])
		} else {
			b.WriteByte('0')
		}
	}
	if v.scale > 0 {
		b.WriteByte('.')
		for i := range v.scale {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			index := point + i
			if index >= 0 && index < int64(len(v.text)) {
				b.WriteByte(v.text[index])
			} else {
				b.WriteByte('0')
			}
		}
	}
	return ctx.Err()
}
func roundNumeric(v Value, scale int64) Value {
	v.scale = scale
	if v.scale < 0 {
		v.scale = 0
	}
	if v.text == "" || v.special != numericFinite {
		return v
	}
	cut := int64(len(v.text)) + v.exponent + scale
	if cut >= int64(len(v.text)) {
		return v
	}
	up := cut >= 0 && v.text[cut] >= '5'
	if cut <= 0 {
		v.text = ""
		v.exponent = 0
		if up {
			v.text = "1"
			v.exponent = -scale
		}
		return normalizeNumeric(v)
	}
	digits := v.text[:cut]
	v.exponent = -scale
	if up {
		buf := []byte(digits)
		i := len(buf) - 1
		for i >= 0 && buf[i] == '9' {
			buf[i] = '0'
			i--
		}
		if i < 0 {
			digits = "1" + string(buf)
		} else {
			buf[i]++
			digits = string(buf)
		}
	}
	v.text = digits
	return normalizeNumeric(v)
}
func sizeNumeric(v Value, typmod int32) (Value, error) {
	if typmod < 0 {
		return v, nil
	}
	p, s := numericParts(typmod)
	if v.special == numericNaN {
		return v, nil
	}
	if v.special != numericFinite {
		return Value{}, numericOverflow(p, s, true)
	}
	v = roundNumeric(v, int64(s))
	if v.text != "" && int64(len(v.text))+v.exponent > int64(p-s) {
		return Value{}, numericOverflow(p, s, false)
	}
	return v, nil
}
func numericOverflow(p, s int32, infinite bool) error {
	detail := fmt.Sprintf("A field with precision %d, scale %d", p, s)
	if infinite {
		detail += " cannot hold an infinite value."
	} else {
		bound := fmt.Sprintf("10^%d", p-s)
		if p == s {
			bound = "1"
		}
		detail += " must round to an absolute value less than " + bound + "."
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "numeric field overflow", Detail: detail}
}

// A comparison view borrows immutable digits or uses the caller's stack buffer.
// Integer promotion for comparison does not allocate a numeric payload.
type numericView struct {
	text     string
	digits   []byte
	exponent int64
	negative bool
	special  uint8
}

func numericComparisonView(v Value, buffer *[20]byte) numericView {
	if v.typ.id == Numeric {
		return numericView{text: v.text, exponent: v.exponent, negative: v.negative, special: v.special}
	}
	if v.integer == 0 {
		return numericView{}
	}
	digits := strconv.AppendInt(buffer[:0], v.integer, 10)
	negative := v.integer < 0
	if negative {
		digits = digits[1:]
	}
	return numericView{digits: digits, negative: negative}
}
func (v numericView) length() int {
	if v.digits != nil {
		return len(v.digits)
	}
	return len(v.text)
}
func (v numericView) digit(i int) byte {
	if i >= v.length() {
		return '0'
	}
	if v.digits != nil {
		return v.digits[i]
	}
	return v.text[i]
}
func exactOperand(v Value) Value {
	if v.typ.id == Numeric {
		return v
	}
	n := v.integer
	v.text = strconv.FormatInt(n, 10)
	v.negative = n < 0
	if v.negative {
		v.text = v.text[1:]
	}
	return normalizeNumeric(v)
}
func compareNumeric(a, b Value) int {
	var ab, bb [20]byte
	av, bv := numericComparisonView(a, &ab), numericComparisonView(b, &bb)
	return compareNumericViews(av, bv)
}

// Borrowed immutable decimal views also serve JSONB numeric primitives.
func compareNumericViews(av, bv numericView) int {
	rank := func(v numericView) int {
		switch v.special {
		case numericNegativeInfinity:
			return 0
		case numericInfinity:
			return 2
		case numericNaN:
			return 3
		}
		return 1
	}
	ar, br := rank(av), rank(bv)
	if ar < br {
		return -1
	}
	if ar > br {
		return 1
	}
	if ar != 1 {
		return 0
	}
	if av.negative != bv.negative {
		if av.negative {
			return -1
		}
		return 1
	}
	result := 0
	aw, bw := int64(av.length())+av.exponent, int64(bv.length())+bv.exponent
	if av.length() == 0 {
		if bv.length() != 0 {
			result = -1
		}
	} else if bv.length() == 0 {
		result = 1
	} else if aw < bw {
		result = -1
	} else if aw > bw {
		result = 1
	} else {
		for i := range max(av.length(), bv.length()) {
			ac, bc := av.digit(i), bv.digit(i)
			if ac < bc {
				result = -1
				break
			}
			if ac > bc {
				result = 1
				break
			}
		}
	}
	if av.negative {
		return -result
	}
	return result
}

// numericInteger rounds an exact coefficient and checks the selected integer
// width without minting a temporary descriptor or retaining decimal scratch.
func numericInteger(v Value, id ID) (int64, error) {
	name, limit := "integer", uint64(1)<<31
	if id == Int2 {
		name, limit = "smallint", 32768
	}
	if id == Int8 {
		name, limit = "bigint", uint64(1)<<63
	}
	if v.special != numericFinite {
		special := "infinity"
		if v.special == numericNaN {
			special = "NaN"
		}
		return 0, failure("0A000", "cannot convert "+special+" to "+name)
	}
	rounded := roundNumeric(v, 0)
	length := int64(len(rounded.text)) + rounded.exponent
	if length > 19 {
		return 0, failure("22003", name+" out of range")
	}
	if !rounded.negative {
		limit--
	}
	var magnitude uint64
	for i := range length {
		digit := uint64(0)
		if i < int64(len(rounded.text)) {
			digit = uint64(rounded.text[i] - '0')
		}
		if magnitude > (limit-digit)/10 {
			return 0, failure("22003", name+" out of range")
		}
		magnitude = magnitude*10 + digit
	}
	n := int64(magnitude)
	if rounded.negative {
		n = -int64(magnitude-1) - 1
	}
	return n, nil
}

func numericCoercion(ctx context.Context, v Value, target Usage) (Value, error) {
	if target.Type.id == Numeric {
		var result Value
		var err error
		if exactNumber(v.typ.id) {
			result = exactOperand(v)
			result.typ = target.Type
		} else {
			precision := 15
			if v.typ.id == Float4 {
				precision = 6
			}
			result, err = target.Type.numericInput(strconv.FormatFloat(v.floating, 'g', precision, 64), -1)
		}
		if err != nil {
			return Value{}, err
		}
		return sizeNumeric(result, target.Typmod)
	}
	if target.Type.id == Int2 || target.Type.id == Int4 || target.Type.id == Int8 {
		n, err := numericInteger(v, target.Type.id)
		if err != nil {
			return Value{}, err
		}
		return Integer(target.Type, n)
	}
	return target.Type.Input(ctx, numericOutput(v), -1)
}
