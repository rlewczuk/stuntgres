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

// Ranges are immutable. Builtin bounds stay inline; custom finite bounds refer
// into owned typed endpoint pairs. Omitted and empty endpoints have zero payloads.
// SQL NULL belongs to Value.null, not the flags or an endpoint sentinel.
type rangeDatum struct {
	lower, upper rangeBound
	flags        uint8
}

type rangeBound struct {
	value            *Value // Interior reference into one immutable endpoint pair.
	coefficient      string
	magnitude, scale int64
	negative         bool
	special          uint8
}

type rangeEndpointPair struct{ lower, upper Value }

func isRange(id ID) bool {
	return id == Int4Range || id == Int8Range || id == NumRange || id == DateRange || id == TimestampRange || id == TimestamptzRange
}

func (d *Descriptor) rangeBoundInput(ctx context.Context, input string, env InputEnvironment) (rangeBound, error) {
	if d.rangeSubtype.id == Numeric {
		v, err := d.rangeSubtype.numericInput(input, -1)
		return rangeBound{coefficient: v.text, magnitude: v.exponent, scale: v.scale, negative: v.negative, special: v.special}, err
	}
	if d.rangeSubtype.id == Date || d.rangeSubtype.id == Timestamp || d.rangeSubtype.id == Timestamptz {
		v, err := d.rangeSubtype.InputWithEnvironment(ctx, input, -1, env)
		return rangeBound{magnitude: v.integer}, err
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

func (d *Descriptor) compareBuiltinRangeBounds(a, b rangeBound) int {
	if d.rangeSubtype.id == Numeric {
		return compareNumericViews(a.numericView(), b.numericView())
	}
	if a.magnitude < b.magnitude {
		return -1
	}
	if a.magnitude > b.magnitude {
		return 1
	}
	return 0
}

func (d *Descriptor) compareRangeBounds(ctx context.Context, a, b rangeBound) (int, error) {
	if d.customRange != nil {
		if a.value == nil || b.value == nil {
			return 0, fmt.Errorf("missing custom range endpoint")
		}
		return d.customRange.Ordering.Compare(ctx, *a.value, *b.value, InputEnvironment{})
	}
	return d.compareBuiltinRangeBounds(a, b), nil
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

func (d *Descriptor) rangeInput(ctx context.Context, input string, env InputEnvironment) (Value, error) {
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
	if d.customRange != nil {
		lo, hi := Null(d.rangeSubtype), Null(d.rangeSubtype)
		if !lowerAbsent {
			lo, err = d.rangeSubtype.InputWithEnvironment(ctx, lower, -1, env)
			if err != nil {
				return Value{}, err
			}
		}
		if !upperAbsent {
			hi, err = d.rangeSubtype.InputWithEnvironment(ctx, upper, -1, env)
			if err != nil {
				return Value{}, err
			}
		}
		return d.rangeFromTypedBounds(ctx, lo, hi, datum.flags, true)
	} else {
		if lowerAbsent {
			datum.flags = datum.flags&^rangeLowerInclusive | rangeLowerUnbounded
		} else {
			bound, err := d.rangeBoundInput(ctx, lower, env)
			if err != nil {
				return Value{}, err
			}
			datum.lower = bound
		}
		if upperAbsent {
			datum.flags = datum.flags&^rangeUpperInclusive | rangeUpperUnbounded
		} else {
			bound, err := d.rangeBoundInput(ctx, upper, env)
			if err != nil {
				return Value{}, err
			}
			datum.upper = bound
		}
	}
	datum, err = d.canonicalRange(ctx, datum, true)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: d, rangeDatum: datum}, nil
}

// Generic families have no canonical function. Shared typed construction and
// operator/binary normalization use the same captured comparison and errors.
func (d *Descriptor) canonicalGenericBounds(ctx context.Context, flags uint8, lower, upper Value, inputSave bool) (uint8, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if flags&rangeEmpty != 0 {
		return rangeEmpty, nil
	}
	if flags&(rangeLowerUnbounded|rangeUpperUnbounded) != 0 {
		return flags, nil
	}
	cmp, err := d.customRange.Ordering.Compare(ctx, lower, upper, InputEnvironment{})
	if err != nil {
		return 0, err
	}
	if cmp > 0 {
		err := failure("22000", "range lower bound must be less than or equal to range upper bound")
		if inputSave {
			err = sqlstate.SoftInputError(err)
		}
		return 0, err
	}
	if cmp == 0 && flags&(rangeLowerInclusive|rangeUpperInclusive) != rangeLowerInclusive|rangeUpperInclusive {
		return rangeEmpty, nil
	}
	return flags, nil
}

// inputSave belongs only to text input, not binary/constructor/operator callers.
func (d *Descriptor) canonicalRange(ctx context.Context, x rangeDatum, inputSave bool) (rangeDatum, error) {
	if x.flags == rangeEmpty {
		return x, nil
	}
	if d.customRange != nil {
		var lower, upper Value
		if x.flags&rangeLowerUnbounded == 0 {
			lower = *x.lower.value
		}
		if x.flags&rangeUpperUnbounded == 0 {
			upper = *x.upper.value
		}
		flags, err := d.canonicalGenericBounds(ctx, x.flags, lower, upper, inputSave)
		if err != nil {
			return rangeDatum{}, err
		}
		if flags == rangeEmpty {
			return rangeDatum{flags: rangeEmpty}, nil
		}
		x.flags = flags
		return x, nil
	}
	lo, hi := x.flags&rangeLowerUnbounded != 0, x.flags&rangeUpperUnbounded != 0
	if !lo && !hi {
		cmp, compareErr := d.compareRangeBounds(ctx, x.lower, x.upper)
		if compareErr != nil {
			return x, compareErr
		}
		if cmp > 0 {
			err := failure("22000", "range lower bound must be less than or equal to range upper bound")
			if inputSave {
				err = sqlstate.SoftInputError(err)
			}
			return x, err
		}
		if cmp == 0 && x.flags&(rangeLowerInclusive|rangeUpperInclusive) != rangeLowerInclusive|rangeUpperInclusive {
			return rangeDatum{flags: rangeEmpty}, nil
		}
	}
	if d.id == NumRange || d.id == TimestampRange || d.id == TimestamptzRange {
		return x, nil
	}
	if d.id == DateRange {
		if !lo && x.flags&rangeLowerInclusive == 0 && finiteRangeDate(x.lower.magnitude) {
			if x.lower.magnitude == dateMaximum {
				return x, failure("22008", "date out of range")
			}
			x.lower.magnitude++
			x.flags |= rangeLowerInclusive
		}
		if !hi && x.flags&rangeUpperInclusive != 0 && finiteRangeDate(x.upper.magnitude) {
			if x.upper.magnitude == dateMaximum {
				return x, failure("22008", "date out of range")
			}
			x.upper.magnitude++
			x.flags &^= rangeUpperInclusive
		}
		if !lo && !hi {
			cmp := d.compareBuiltinRangeBounds(x.lower, x.upper)
			if cmp > 0 || cmp == 0 && x.flags&(rangeLowerInclusive|rangeUpperInclusive) != rangeLowerInclusive|rangeUpperInclusive {
				return rangeDatum{flags: rangeEmpty}, nil
			}
		}
		return x, nil
	}
	maximum, name := int64(math.MaxInt32), "integer"
	if d.id == Int8Range {
		maximum, name = math.MaxInt64, "bigint"
	}
	if !lo && x.flags&rangeLowerInclusive == 0 {
		if x.lower.magnitude == maximum {
			return x, failure("22003", name+" out of range")
		}
		x.lower.magnitude++
		x.flags |= rangeLowerInclusive
	}
	if !hi && x.flags&rangeUpperInclusive != 0 {
		if x.upper.magnitude == maximum {
			return x, failure("22003", name+" out of range")
		}
		x.upper.magnitude++
		x.flags &^= rangeUpperInclusive
	}
	if !lo && !hi && x.lower.magnitude >= x.upper.magnitude {
		x = rangeDatum{flags: rangeEmpty}
	}
	return x, nil
}

func (b rangeBound) validNumeric(d *Descriptor) bool {
	if b.special > numericNaN || b.scale < 0 || b.scale > 16383 {
		return false
	}
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
		if b.coefficient[i] < '0' || b.coefficient[i] > '9' {
			return false
		}
	}
	// Bound the exponent before arithmetic in the existing numeric validator.
	if b.magnitude > 131072 {
		return false
	}
	return validateNumericFormat(b.numericValue(d)) == nil
}

func (v Value) validRange() bool {
	if v.typ == nil || v.null {
		return false
	}
	d := v.typ.BaseType()
	if !d.IsRange() || d.rangeSubtype == nil {
		return false
	}
	return d.validRangeDatum(v.rangeDatum)
}

func (d *Descriptor) validRangeDatum(r rangeDatum) bool {
	if !d.IsRange() || d.rangeSubtype == nil {
		return false
	}
	if r.flags&rangeEmpty != 0 {
		return r == (rangeDatum{flags: rangeEmpty})
	}
	if r.flags&^(rangeLowerInclusive|rangeUpperInclusive|rangeLowerUnbounded|rangeUpperUnbounded) != 0 {
		return false
	}
	lowerAbsent, upperAbsent := r.flags&rangeLowerUnbounded != 0, r.flags&rangeUpperUnbounded != 0
	if lowerAbsent && (r.lower != (rangeBound{}) || r.flags&rangeLowerInclusive != 0) {
		return false
	}
	if upperAbsent && (r.upper != (rangeBound{}) || r.flags&rangeUpperInclusive != 0) {
		return false
	}
	if d.customRange != nil {
		valid := func(b rangeBound, absent bool) bool {
			if absent {
				return b == (rangeBound{})
			}
			return b.value != nil && b == (rangeBound{value: b.value}) && b.value.typ == d.rangeSubtype && !b.value.null
		}
		return valid(r.lower, lowerAbsent) && valid(r.upper, upperAbsent)
	}
	if d.id == NumRange {
		if !lowerAbsent && !r.lower.validNumeric(d.rangeSubtype) || !upperAbsent && !r.upper.validNumeric(d.rangeSubtype) {
			return false
		}
		if lowerAbsent || upperAbsent {
			return true
		}
		cmp := d.compareBuiltinRangeBounds(r.lower, r.upper)
		return cmp < 0 || cmp == 0 && r.flags&(rangeLowerInclusive|rangeUpperInclusive) == rangeLowerInclusive|rangeUpperInclusive
	}
	if d.id == TimestampRange || d.id == TimestamptzRange {
		if !lowerAbsent && !validRangeTimestampBound(r.lower) || !upperAbsent && !validRangeTimestampBound(r.upper) {
			return false
		}
		if lowerAbsent || upperAbsent {
			return true
		}
		cmp := d.compareBuiltinRangeBounds(r.lower, r.upper)
		return cmp < 0 || cmp == 0 && r.flags&(rangeLowerInclusive|rangeUpperInclusive) == rangeLowerInclusive|rangeUpperInclusive
	}
	if d.id == DateRange {
		if !lowerAbsent && !validRangeDateBound(r.lower) || !upperAbsent && !validRangeDateBound(r.upper) {
			return false
		}
		if !lowerAbsent && finiteRangeDate(r.lower.magnitude) && r.flags&rangeLowerInclusive == 0 ||
			!upperAbsent && finiteRangeDate(r.upper.magnitude) && r.flags&rangeUpperInclusive != 0 {
			return false
		}
		if lowerAbsent || upperAbsent {
			return true
		}
		cmp := d.compareBuiltinRangeBounds(r.lower, r.upper)
		return cmp < 0 || cmp == 0 && r.flags&(rangeLowerInclusive|rangeUpperInclusive) == rangeLowerInclusive|rangeUpperInclusive
	}
	if r.flags&rangeUpperInclusive != 0 || !lowerAbsent && r.flags&rangeLowerInclusive == 0 {
		return false
	}
	if r.lower != (rangeBound{magnitude: r.lower.magnitude}) || r.upper != (rangeBound{magnitude: r.upper.magnitude}) {
		return false
	}
	if d.id == Int4Range && (r.lower.magnitude < math.MinInt32 || r.lower.magnitude > math.MaxInt32 || r.upper.magnitude < math.MinInt32 || r.upper.magnitude > math.MaxInt32) {
		return false
	}
	return lowerAbsent || upperAbsent || r.lower.magnitude < r.upper.magnitude
}

func (v Value) rangeOutput(ctx context.Context, env OutputEnvironment) (string, error) {
	if !v.validRange() {
		return "", fmt.Errorf("invalid range datum")
	}
	r := v.rangeDatum
	if r.flags == rangeEmpty {
		return "empty", nil
	}
	var out strings.Builder
	if err := v.typ.BaseType().appendRangeDatum(ctx, &out, r, env); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (d *Descriptor) appendRangeDatum(ctx context.Context, out *strings.Builder, r rangeDatum, env OutputEnvironment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.customRange == nil {
		d.writeRangeDatum(out, r, env)
		return nil
	}
	left, right := byte('('), byte(')')
	if r.flags&rangeLowerInclusive != 0 {
		left = '['
	}
	if r.flags&rangeUpperInclusive != 0 {
		right = ']'
	}
	out.WriteByte(left)
	if r.flags&rangeLowerUnbounded == 0 {
		text, err := d.rangeSubtype.OutputWithEnvironment(ctx, *r.lower.value, env)
		if err != nil {
			return err
		}
		writeRangeBoundText(out, text)
	}
	out.WriteByte(',')
	if r.flags&rangeUpperUnbounded == 0 {
		text, err := d.rangeSubtype.OutputWithEnvironment(ctx, *r.upper.value, env)
		if err != nil {
			return err
		}
		writeRangeBoundText(out, text)
	}
	out.WriteByte(right)
	return nil
}

// Write directly into the caller's builder, avoiding a string per integer member.
func (d *Descriptor) writeRangeDatum(out *strings.Builder, r rangeDatum, env OutputEnvironment) {
	left, right := byte('('), byte(')')
	if r.flags&rangeLowerInclusive != 0 {
		left = '['
	}
	if r.flags&rangeUpperInclusive != 0 {
		right = ']'
	}
	if d.id == NumRange {
		lower, upper := "", ""
		if r.flags&rangeLowerUnbounded == 0 {
			lower = numericOutput(r.lower.numericValue(d.rangeSubtype))
		}
		if r.flags&rangeUpperUnbounded == 0 {
			upper = numericOutput(r.upper.numericValue(d.rangeSubtype))
		}
		out.Grow(len(lower) + len(upper) + 3)
		out.WriteByte(left)
		out.WriteString(lower)
		out.WriteByte(',')
		out.WriteString(upper)
		out.WriteByte(right)
		return
	}
	if d.id == DateRange || d.id == TimestampRange || d.id == TimestamptzRange {
		out.WriteByte(left)
		if r.flags&rangeLowerUnbounded == 0 {
			d.writeRangeTemporalBound(out, r.lower.magnitude, env)
		}
		out.WriteByte(',')
		if r.flags&rangeUpperUnbounded == 0 {
			d.writeRangeTemporalBound(out, r.upper.magnitude, env)
		}
		out.WriteByte(right)
		return
	}
	var buffer [44]byte
	bytes := append(buffer[:0], left)
	if r.flags&rangeLowerUnbounded == 0 {
		bytes = strconv.AppendInt(bytes, r.lower.magnitude, 10)
	}
	bytes = append(bytes, ',')
	if r.flags&rangeUpperUnbounded == 0 {
		bytes = strconv.AppendInt(bytes, r.upper.magnitude, 10)
	}
	bytes = append(bytes, right)
	out.Write(bytes)
}

func finiteRangeDate(day int64) bool {
	return day != DateNegativeInfinity && day != DatePositiveInfinity
}

func validRangeDateBound(b rangeBound) bool {
	return b == (rangeBound{magnitude: b.magnitude}) &&
		(!finiteRangeDate(b.magnitude) || b.magnitude >= dateMinimum && b.magnitude <= dateMaximum)
}

func validRangeTimestampBound(b rangeBound) bool {
	return b == (rangeBound{magnitude: b.magnitude}) &&
		(b.magnitude == TimestampNegativeInfinity || b.magnitude == TimestampPositiveInfinity ||
			b.magnitude >= timestampMinimum && b.magnitude <= timestampEnd)
}

func (d *Descriptor) writeRangeTemporalBound(out *strings.Builder, value int64, env OutputEnvironment) {
	var text string
	if d.id == TimestampRange {
		text = timestampStyledOutput(value, env.DateStyle, "", 0, false)
	} else if d.id == TimestamptzRange {
		text = timestamptzOutput(value, env)
	} else {
		text = dateStyledOutput(value, env.DateStyle)
	}
	writeRangeBoundText(out, text)
}

func writeRangeBoundText(out *strings.Builder, text string) {
	quoted := len(text) == 0
	for i := range len(text) {
		c := text[i]
		if c == '(' || c == ')' || c == '[' || c == ']' || c == ',' || c == '"' || c == '\\' || space(c) {
			quoted = true
		}
	}
	if quoted {
		out.WriteByte('"')
	}
	for i := range len(text) {
		c := text[i]
		if c == '"' || c == '\\' {
			out.WriteByte(c)
		}
		out.WriteByte(c)
	}
	if quoted {
		out.WriteByte('"')
	}
}
