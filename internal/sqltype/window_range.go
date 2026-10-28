package sqltype

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqlstate"
)

// RangeSupport is immutable B-tree support #3 authority. Logical key usages stay
// with their query expressions; the predicate unwraps domain tags, not payloads.
type RangeSupport struct {
	key, offset, timestamp *Descriptor
	class                  uint32
}

var rangeIntegerOffsets = [...]ID{Int2, Int4, Int8}
var rangeWideOffset = [...]ID{Int8}
var rangeFloatOffset = [...]ID{Float8}
var rangeNumericOffset = [...]ID{Numeric}
var rangeIntervalOffset = [...]ID{Interval}

func rangeCandidates(key *Descriptor) []ID {
	switch key.BaseType().id {
	case Int2, Int4:
		return rangeIntegerOffsets[:]
	case Int8:
		return rangeWideOffset[:]
	case Float4, Float8:
		return rangeFloatOffset[:]
	case Numeric:
		return rangeNumericOffset[:]
	case Date, Timestamp, Timestamptz, Time, Timetz, Interval:
		return rangeIntervalOffset[:]
	}
	return nil
}

func rangeInputDiagnosticName(class IndexClass, lookup func(ID) *Descriptor) string {
	if class.enum {
		return "anyenum"
	}
	return lookup(class.input).DiagnosticName()
}

// BindRange selects among the released support registrations using ordinary
// implicit coercion. A domain is flattened for coercibility, not exact-match
// preference: integer-domain offsets therefore retain PostgreSQL ambiguity.
// Diagnostic spelling belongs to the caller's coherent catalog visibility.
func BindRange(key Usage, source *Descriptor, lookup func(ID) *Descriptor, typeName func(*Descriptor) string) (RangeSupport, Usage, error) {
	class, ok := DefaultIndexClass(key.Type)
	if !ok {
		return RangeSupport{}, Usage{}, fmt.Errorf("invalid RANGE ordering class")
	}
	candidates := rangeCandidates(key.Type)
	if len(candidates) == 0 {
		return RangeSupport{}, Usage{}, failure("0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type "+rangeInputDiagnosticName(class, lookup))
	}
	preferred := key.Type.BaseType().id
	if source != nil {
		preferred = source.id
	}
	matches := 0
	var chosen *Descriptor
	for _, id := range candidates {
		target := lookup(id)
		if source != nil {
			if _, err := PlanCoercion(source.Usage(), target.Usage(), Implicit); err != nil {
				continue
			}
		}
		matches++
		if chosen == nil || id == preferred {
			chosen = target
		}
	}
	if matches == 0 || matches > 1 && chosen.id != preferred {
		inputName := rangeInputDiagnosticName(class, lookup)
		sourceName := "unknown"
		if source != nil {
			sourceName = typeName(source)
		}
		if matches == 0 {
			return RangeSupport{}, Usage{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "RANGE with offset PRECEDING/FOLLOWING is not supported for column type " + inputName + " and offset type " + sourceName, Hint: "Cast the offset value to an appropriate type."}
		}
		return RangeSupport{}, Usage{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "RANGE with offset PRECEDING/FOLLOWING has multiple interpretations for column type " + inputName + " and offset type " + sourceName, Hint: "Cast the offset value to the exact intended type."}
	}
	timestamp := key.Type.BaseType()
	if timestamp.id == Date {
		timestamp = lookup(Timestamp)
	}
	return RangeSupport{key.Type.BaseType(), chosen, timestamp, class.OID()}, chosen.Usage(), nil
}

func rangeInvalidOffset() error {
	return failure("22013", "invalid preceding or following size in window function")
}
func rangeComparison(sign int, less bool) bool {
	if less {
		return sign <= 0
	}
	return sign >= 0
}
func rangeIntComparison(a, b int64, less bool) bool {
	if less {
		return a <= b
	}
	return a >= b
}

func (p RangeSupport) InRange(ctx context.Context, val, base, offset Value, sub, less bool, input InputEnvironment) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if p.key == nil || p.offset == nil || val.null || base.null || offset.null || val.typ.BaseType() != p.key || base.typ.BaseType() != p.key || offset.typ != p.offset {
		return false, fmt.Errorf("invalid RANGE support operands")
	}
	val.typ, base.typ = p.key, p.key
	switch p.key.id {
	case Int2, Int4, Int8:
		if offset.integer < 0 {
			return false, rangeInvalidOffset()
		}
		n := offset.integer
		if sub {
			if base.integer < math.MinInt64+n {
				return !less, nil
			}
			return rangeIntComparison(val.integer, base.integer-n, less), ctx.Err()
		}
		if base.integer > math.MaxInt64-n {
			return less, nil
		}
		return rangeIntComparison(val.integer, base.integer+n, less), ctx.Err()
	case Float4, Float8:
		a, b, o := val.floating, base.floating, offset.floating
		if math.IsNaN(o) || o < 0 {
			return false, rangeInvalidOffset()
		}
		if math.IsNaN(a) {
			return math.IsNaN(b) || !less, nil
		}
		if math.IsNaN(b) {
			return less, nil
		}
		if math.IsInf(o, 1) && (sub && math.IsInf(b, 1) || !sub && math.IsInf(b, -1)) {
			return true, nil
		}
		if sub {
			b -= o
		} else {
			b += o
		}
		if less {
			return a <= b, ctx.Err()
		}
		return a >= b, ctx.Err()
	case Numeric:
		if offset.special == numericNaN || offset.special == numericNegativeInfinity || offset.negative {
			return false, rangeInvalidOffset()
		}
		if val.special == numericNaN {
			return base.special == numericNaN || !less, nil
		}
		if base.special == numericNaN {
			return less, nil
		}
		if offset.special == numericInfinity {
			if sub && base.special == numericInfinity || !sub && base.special == numericNegativeInfinity {
				return true, nil
			}
			if sub {
				return !less || val.special == numericNegativeInfinity, nil
			}
			return less || val.special == numericInfinity, nil
		}
		if val.special != numericFinite || base.special != numericFinite {
			return rangeComparison(compareNumeric(val, base), less), ctx.Err()
		}
		if sub {
			offset = numericNegate(offset)
		}
		bound, err := numericAdd(ctx, base, offset)
		if err != nil {
			return false, err
		}
		return rangeComparison(compareNumeric(val, bound), less), ctx.Err()
	case Time, Timetz:
		if offset.integer < 0 {
			return false, rangeInvalidOffset()
		}
		bound := base
		if sub {
			bound.integer -= offset.integer
		} else {
			var ok bool
			bound.integer, ok = intervalAdd(base.integer, offset.integer)
			if !ok {
				return less, nil
			}
		}
		if p.key.id == Time {
			return rangeIntComparison(val.integer, bound.integer, less), ctx.Err()
		}
		// Rotate mathematical (non-wrapping) clocks without overflowing a synthetic
		// int64 clock near infinity, then retain the released fixed-zone tie rule.
		vh, vl := intervalSpan(Value{integer: val.integer, days: 0})
		bh, bl := intervalSpan(Value{integer: bound.integer, days: 0})
		vh, vl = rangeAddZone(vh, vl, val.west)
		bh, bl = rangeAddZone(bh, bl, bound.west)
		sign := 0
		if vh < bh || vh == bh && vl < bl {
			sign = -1
		} else if vh > bh || vh == bh && vl > bl {
			sign = 1
		} else if val.west < bound.west {
			sign = -1
		} else if val.west > bound.west {
			sign = 1
		}
		return rangeComparison(sign, less), ctx.Err()
	case Date, Timestamp, Timestamptz, Interval:
		if p.key.id == Date {
			var err error
			val.integer, err = arithmeticDateMidnight(val)
			if err != nil {
				return false, err
			}
			base.integer, err = arithmeticDateMidnight(base)
			if err != nil {
				return false, err
			}
			val.typ, base.typ = p.timestamp, p.timestamp
		}
		hi, _ := intervalSpan(offset)
		if intervalInfinity(offset) < 0 || hi < 0 {
			return false, rangeInvalidOffset()
		}
		if intervalInfinity(offset) > 0 {
			infinity := timestampInfinity(base.integer)
			if p.key.id == Interval {
				infinity = intervalInfinity(base)
			}
			if sub && infinity > 0 || !sub && infinity < 0 {
				return true, nil
			}
		}
		var bound Value
		var err error
		if p.key.id == Interval {
			bound, err = addIntervals(base, offset, p.key, sub)
		} else {
			bound, err = addTimestampInterval(ctx, base.integer, offset, p.timestamp, sub, input)
		}
		if err != nil {
			return false, err
		}
		if p.key.id == Interval {
			return rangeComparison(compareIntervals(val, bound), less), ctx.Err()
		}
		return rangeIntComparison(val.integer, bound.integer, less), ctx.Err()
	}
	return false, fmt.Errorf("invalid RANGE support registration")
}

func rangeAddZone(hi int64, lo uint64, west int32) (int64, uint64) {
	delta := int64(west) * 1000000
	next := lo + uint64(delta)
	carry := int64(0)
	if next < lo {
		carry = 1
	}
	return hi + (delta >> 63) + carry, next
}

// Remap rebinds an already selected registration to the replacement catalog's
// descriptors. It never re-resolves an inferred source or retains old authority.
func (p RangeSupport) Remap(mapper func(*Descriptor) *Descriptor) (RangeSupport, error) {
	if p.key == nil {
		return p, nil
	}
	p.key, p.offset, p.timestamp = mapper(p.key), mapper(p.offset), mapper(p.timestamp)
	class, ok := DefaultIndexClass(p.key)
	if !ok || class.OID() != p.class {
		return RangeSupport{}, fmt.Errorf("invalid remapped RANGE class")
	}
	for _, id := range rangeCandidates(p.key) {
		if id == p.offset.id {
			return p, nil
		}
	}
	return RangeSupport{}, fmt.Errorf("invalid remapped RANGE registration")
}
