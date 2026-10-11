package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

const (
	rangeEmpty uint8 = 1 << iota
	rangeLowerInclusive
	rangeUpperInclusive
	rangeLowerUnbounded
	rangeUpperUnbounded
)

// Ranges are inline and immutable. Endpoint interpretation belongs to the
// descriptor's subtype; omitted and empty endpoints have entirely zero payloads.
// SQL NULL belongs to Value.null, not the flags or an endpoint sentinel.
type rangeDatum struct {
	lower, upper rangeBound
	flags uint8
}

type rangeBound struct {
	coefficient string
	magnitude, scale int64
	negative bool
	special uint8
}

func isRange(id ID) bool { return id == Int4Range || id == Int8Range || id == NumRange }

func (d *Descriptor) rangeBoundInput(input string) (rangeBound, error) {
	if d.rangeSubtype.id == Numeric {
		v, err := d.rangeSubtype.numericInput(input, -1)
		return rangeBound{v.text, v.exponent, v.scale, v.negative, v.special}, err
	}
	v, err := d.rangeSubtype.integerInput(input)
	return rangeBound{magnitude: v.integer}, err
}

func (b rangeBound) numericValue(d *Descriptor) Value {
	return Value{typ: d, text: b.coefficient, exponent: b.magnitude, scale: b.scale, negative: b.negative, special: b.special}
}

func (b rangeBound) numericView() numericView {
	return numericView{text: b.coefficient, exponent: b.magnitude, negative: b.negative, special: b.special}
}

func (d *Descriptor) compareRangeBounds(a, b rangeBound) int {
	if d.rangeSubtype.id == Numeric { return compareNumericViews(a.numericView(), b.numericView()) }
	if a.magnitude < b.magnitude { return -1 }
	if a.magnitude > b.magnitude { return 1 }
	return 0
}

func malformedRange(input, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed range literal: "` + input + `"`, Detail: detail}
}

// scanRangeBound consumes quoted fragments and escaped bytes. Plain bounds
// borrow their input slice; only actual decoding allocates a builder payload.
func scanRangeBound(ctx context.Context, input string, at int) (string, int, error) {
	start, fragment := at, at
	quoted, decoded := false, false
	nextCheck := at
	var out strings.Builder
	for at < len(input) {
		if at >= nextCheck {
			if err := ctx.Err(); err != nil {
				return "", at, err
			}
			nextCheck = at + 1024
		}
		c := input[at]
		if c == '\\' {
			if at+1 == len(input) {
				return "", at, malformedRange(input, "Unexpected end of input.")
			}
			out.WriteString(input[fragment:at])
			out.WriteByte(input[at+1])
			decoded = true
			at += 2
			fragment = at
			continue
		}
		if c == '"' {
			out.WriteString(input[fragment:at])
			decoded = true
			if quoted && at+1 < len(input) && input[at+1] == '"' {
				out.WriteByte('"')
				at += 2
			} else {
				quoted = !quoted
				at++
			}
			fragment = at
			continue
		}
		if !quoted && (c == ',' || c == ')' || c == ']') {
			if !decoded {
				return input[start:at], at, nil
			}
			out.WriteString(input[fragment:at])
			return out.String(), at, nil
		}
		at++
	}
	return "", at, malformedRange(input, "Unexpected end of input.")
}

func (d *Descriptor) rangeInput(ctx context.Context, input string) (Value, error) {
	at := 0
	for at < len(input) && space(input[at]) {
		at++
	}
	if len(input)-at >= 5 && strings.EqualFold(input[at:at+5], "empty") {
		at += 5
		for at < len(input) && space(input[at]) {
			at++
		}
		if at != len(input) {
			return Value{}, malformedRange(input, `Junk after "empty" key word.`)
		}
		return Value{typ: d, rangeDatum: rangeDatum{flags: rangeEmpty}}, nil
	}
	if at == len(input) || input[at] != '(' && input[at] != '[' {
		return Value{}, malformedRange(input, "Missing left parenthesis or bracket.")
	}
	var datum rangeDatum
	if input[at] == '[' {
		datum.flags |= rangeLowerInclusive
	}
	at++
	lowerAbsent := at < len(input) && input[at] == ','
	lower, at, err := scanRangeBound(ctx, input, at)
	if err != nil {
		return Value{}, err
	}
	if input[at] != ',' {
		return Value{}, malformedRange(input, "Missing comma after lower bound.")
	}
	at++
	upperAbsent := at < len(input) && (input[at] == ')' || input[at] == ']')
	upper, at, err := scanRangeBound(ctx, input, at)
	if err != nil {
		return Value{}, err
	}
	if input[at] == ',' {
		return Value{}, malformedRange(input, "Too many commas.")
	}
	if input[at] == ']' {
		datum.flags |= rangeUpperInclusive
	}
	at++
	for at < len(input) && space(input[at]) {
		at++
	}
	if at != len(input) {
		return Value{}, malformedRange(input, "Junk after right parenthesis or bracket.")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if d.rangeSubtype == nil {
		return Value{}, fmt.Errorf("missing range subtype")
	}
	if lowerAbsent {
		datum.flags = datum.flags&^rangeLowerInclusive | rangeLowerUnbounded
	} else {
		bound, err := d.rangeBoundInput(lower)
		if err != nil { return Value{}, err }
		datum.lower = bound
	}
	if upperAbsent {
		datum.flags = datum.flags&^rangeUpperInclusive | rangeUpperUnbounded
	} else {
		bound, err := d.rangeBoundInput(upper)
		if err != nil { return Value{}, err }
		datum.upper = bound
	}
	if !lowerAbsent && !upperAbsent {
		cmp := d.compareRangeBounds(datum.lower, datum.upper)
		if cmp > 0 {
			return Value{}, failure("22000", "range lower bound must be less than or equal to range upper bound")
		}
		if cmp == 0 && datum.flags&(rangeLowerInclusive|rangeUpperInclusive) != rangeLowerInclusive|rangeUpperInclusive {
			return Value{typ: d, rangeDatum: rangeDatum{flags: rangeEmpty}}, nil
		}
	}
	if d.id == NumRange { return Value{typ: d, rangeDatum: datum}, nil }
	maximum, name := int64(math.MaxInt32), "integer"
	if d.id == Int8Range {
		maximum, name = math.MaxInt64, "bigint"
	}
	if !lowerAbsent && datum.flags&rangeLowerInclusive == 0 {
		if datum.lower.magnitude == maximum {
			return Value{}, failure("22003", name+" out of range")
		}
		datum.lower.magnitude++
		datum.flags |= rangeLowerInclusive
	}
	if !upperAbsent && datum.flags&rangeUpperInclusive != 0 {
		if datum.upper.magnitude == maximum {
			return Value{}, failure("22003", name+" out of range")
		}
		datum.upper.magnitude++
		datum.flags &^= rangeUpperInclusive
	}
	if !lowerAbsent && !upperAbsent && datum.lower.magnitude >= datum.upper.magnitude {
		datum = rangeDatum{flags: rangeEmpty}
	}
	return Value{typ: d, rangeDatum: datum}, nil
}

func (b rangeBound) validNumeric(d *Descriptor) bool {
	if b.special > numericNaN || b.scale < 0 || b.scale > 16383 { return false }
	if b.special != numericFinite {
		return b.coefficient == "" && b.magnitude == 0 && b.scale == 0 && !b.negative
	}
	if b.coefficient == "" {
		return b.magnitude == 0 && !b.negative
	}
	if b.coefficient[0] == '0' || b.coefficient[len(b.coefficient)-1] == '0' || b.magnitude < -b.scale {
		return false
	}
	for i := range b.coefficient {
		if b.coefficient[i] < '0' || b.coefficient[i] > '9' { return false }
	}
	// Bound the exponent before arithmetic in the existing numeric validator.
	if b.magnitude > 131072 { return false }
	return validateNumericFormat(b.numericValue(d)) == nil
}

func (v Value) validRange() bool {
	if v.typ == nil || v.null { return false }
	d := v.typ.BaseType()
	if !isRange(d.id) || d.rangeSubtype == nil { return false }
	r := v.rangeDatum
	if r.flags&rangeEmpty != 0 { return r == (rangeDatum{flags: rangeEmpty}) }
	if r.flags&^(rangeLowerInclusive|rangeUpperInclusive|rangeLowerUnbounded|rangeUpperUnbounded) != 0 { return false }
	lowerAbsent, upperAbsent := r.flags&rangeLowerUnbounded != 0, r.flags&rangeUpperUnbounded != 0
	if lowerAbsent && (r.lower != (rangeBound{}) || r.flags&rangeLowerInclusive != 0) { return false }
	if upperAbsent && (r.upper != (rangeBound{}) || r.flags&rangeUpperInclusive != 0) { return false }
	if d.id == NumRange {
		if !lowerAbsent && !r.lower.validNumeric(d.rangeSubtype) || !upperAbsent && !r.upper.validNumeric(d.rangeSubtype) { return false }
		if lowerAbsent || upperAbsent { return true }
		cmp := d.compareRangeBounds(r.lower, r.upper)
		return cmp < 0 || cmp == 0 && r.flags&(rangeLowerInclusive|rangeUpperInclusive) == rangeLowerInclusive|rangeUpperInclusive
	}
	if r.flags&rangeUpperInclusive != 0 || !lowerAbsent && r.flags&rangeLowerInclusive == 0 { return false }
	if r.lower != (rangeBound{magnitude: r.lower.magnitude}) || r.upper != (rangeBound{magnitude: r.upper.magnitude}) { return false }
	if d.id == Int4Range && (r.lower.magnitude < math.MinInt32 || r.lower.magnitude > math.MaxInt32 || r.upper.magnitude < math.MinInt32 || r.upper.magnitude > math.MaxInt32) { return false }
	return lowerAbsent || upperAbsent || r.lower.magnitude < r.upper.magnitude
}

func (v Value) rangeOutput() (string, error) {
	if !v.validRange() { return "", fmt.Errorf("invalid range datum") }
	r := v.rangeDatum
	if r.flags == rangeEmpty { return "empty", nil }
	left, right := byte('('), byte(')')
	if r.flags&rangeLowerInclusive != 0 { left = '[' }
	if r.flags&rangeUpperInclusive != 0 { right = ']' }
	if v.typ.BaseType().id == NumRange {
		subtype := v.typ.BaseType().rangeSubtype
		lower, upper := "", ""
		if r.flags&rangeLowerUnbounded == 0 { lower = numericOutput(r.lower.numericValue(subtype)) }
		if r.flags&rangeUpperUnbounded == 0 { upper = numericOutput(r.upper.numericValue(subtype)) }
		var out strings.Builder
		out.Grow(len(lower) + len(upper) + 3)
		out.WriteByte(left)
		out.WriteString(lower)
		out.WriteByte(',')
		out.WriteString(upper)
		out.WriteByte(right)
		return out.String(), nil
	}
	var buffer [44]byte
	out := append(buffer[:0], left)
	if r.flags&rangeLowerUnbounded == 0 { out = strconv.AppendInt(out, r.lower.magnitude, 10) }
	out = append(out, ',')
	if r.flags&rangeUpperUnbounded == 0 { out = strconv.AppendInt(out, r.upper.magnitude, 10) }
	out = append(out, right)
	return string(out), nil
}
