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

// Integer ranges are inline, immutable and canonical. Absent endpoints are zero;
// SQL NULL belongs to Value.null, not the flags or any endpoint sentinel.
type integerRangeDatum struct {
	lower, upper int64
	flags        uint8
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

func (d *Descriptor) integerRangeInput(ctx context.Context, input string) (Value, error) {
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
		return Value{typ: d, rangeDatum: integerRangeDatum{flags: rangeEmpty}}, nil
	}
	if at == len(input) || input[at] != '(' && input[at] != '[' {
		return Value{}, malformedRange(input, "Missing left parenthesis or bracket.")
	}
	var datum integerRangeDatum
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
		return Value{}, fmt.Errorf("missing integer range subtype")
	}
	if lowerAbsent {
		datum.flags = datum.flags&^rangeLowerInclusive | rangeLowerUnbounded
	} else {
		v, err := d.rangeSubtype.integerInput(lower)
		if err != nil {
			return Value{}, err
		}
		datum.lower = v.integer
	}
	if upperAbsent {
		datum.flags = datum.flags&^rangeUpperInclusive | rangeUpperUnbounded
	} else {
		v, err := d.rangeSubtype.integerInput(upper)
		if err != nil {
			return Value{}, err
		}
		datum.upper = v.integer
	}
	if !lowerAbsent && !upperAbsent {
		if datum.lower > datum.upper {
			return Value{}, failure("22000", "range lower bound must be less than or equal to range upper bound")
		}
		if datum.lower == datum.upper && datum.flags&(rangeLowerInclusive|rangeUpperInclusive) != rangeLowerInclusive|rangeUpperInclusive {
			return Value{typ: d, rangeDatum: integerRangeDatum{flags: rangeEmpty}}, nil
		}
	}
	maximum, name := int64(math.MaxInt32), "integer"
	if d.id == Int8Range {
		maximum, name = math.MaxInt64, "bigint"
	}
	if !lowerAbsent && datum.flags&rangeLowerInclusive == 0 {
		if datum.lower == maximum {
			return Value{}, failure("22003", name+" out of range")
		}
		datum.lower++
		datum.flags |= rangeLowerInclusive
	}
	if !upperAbsent && datum.flags&rangeUpperInclusive != 0 {
		if datum.upper == maximum {
			return Value{}, failure("22003", name+" out of range")
		}
		datum.upper++
		datum.flags &^= rangeUpperInclusive
	}
	if !lowerAbsent && !upperAbsent && datum.lower >= datum.upper {
		datum = integerRangeDatum{flags: rangeEmpty}
	}
	return Value{typ: d, rangeDatum: datum}, nil
}

func (v Value) validIntegerRange() bool {
	if v.typ == nil || v.null {
		return false
	}
	id := v.typ.BaseType().id
	if id != Int4Range && id != Int8Range {
		return false
	}
	r := v.rangeDatum
	if r.flags&rangeEmpty != 0 {
		return r == (integerRangeDatum{flags: rangeEmpty})
	}
	if r.flags&^(rangeLowerInclusive|rangeLowerUnbounded|rangeUpperUnbounded) != 0 {
		return false
	}
	if r.flags&rangeLowerUnbounded != 0 {
		if r.lower != 0 || r.flags&rangeLowerInclusive != 0 {
			return false
		}
	} else if r.flags&rangeLowerInclusive == 0 {
		return false
	}
	if r.flags&rangeUpperUnbounded != 0 && r.upper != 0 {
		return false
	}
	if id == Int4Range && (r.lower < math.MinInt32 || r.lower > math.MaxInt32 || r.upper < math.MinInt32 || r.upper > math.MaxInt32) {
		return false
	}
	return r.flags&(rangeLowerUnbounded|rangeUpperUnbounded) != 0 || r.lower < r.upper
}

func (v Value) integerRangeOutput() (string, error) {
	if !v.validIntegerRange() {
		return "", fmt.Errorf("invalid integer range datum")
	}
	r := v.rangeDatum
	if r.flags == rangeEmpty {
		return "empty", nil
	}
	var buffer [44]byte
	out := buffer[:0]
	if r.flags&rangeLowerUnbounded != 0 {
		out = append(out, '(')
	} else {
		out = append(out, '[')
		out = strconv.AppendInt(out, r.lower, 10)
	}
	out = append(out, ',')
	if r.flags&rangeUpperUnbounded == 0 {
		out = strconv.AppendInt(out, r.upper, 10)
	}
	out = append(out, ')')
	return string(out), nil
}
