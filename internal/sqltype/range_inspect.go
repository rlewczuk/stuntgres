package sqltype

import (
	"context"
	"fmt"
)

// RangeInspectOperation is a scalar operation over codec-published canonical data.
type RangeInspectOperation uint8

const (
	RangeLower RangeInspectOperation = iota
	RangeUpper
	RangeIsEmpty
	RangeLowerInclusive
	RangeUpperInclusive
	RangeLowerInfinite
	RangeUpperInfinite
	RangeMerge
	RangeConstruct
)

func (o RangeInspectOperation) Name() string {
	switch o {
	case RangeLower:
		return "lower"
	case RangeUpper:
		return "upper"
	case RangeIsEmpty:
		return "isempty"
	case RangeLowerInclusive:
		return "lower_inc"
	case RangeUpperInclusive:
		return "upper_inc"
	case RangeLowerInfinite:
		return "lower_inf"
	case RangeUpperInfinite:
		return "upper_inf"
	case RangeMerge:
		return "range_merge"
	case RangeConstruct:
		return "multirange"
	}
	return ""
}
func RangeInspectKind(name string) (RangeInspectOperation, bool) {
	switch name {
	case "lower":
		return RangeLower, true
	case "upper":
		return RangeUpper, true
	case "isempty":
		return RangeIsEmpty, true
	case "lower_inc":
		return RangeLowerInclusive, true
	case "upper_inc":
		return RangeUpperInclusive, true
	case "lower_inf":
		return RangeLowerInfinite, true
	case "upper_inf":
		return RangeUpperInfinite, true
	case "range_merge":
		return RangeMerge, true
	case "multirange":
		return RangeConstruct, true
	}
	return 0, false
}
func IsRangeInspectType(d *Descriptor) bool {
	return d != nil && (d.BaseType().IsRange() || d.BaseType().IsMultirange())
}

// RangeInspectPlan retains exact input/result authority, never borrowed mutable
// endpoints. Published multiranges expose only their first and last members here.
type RangeInspectPlan struct {
	input, result, rangeType *Descriptor
	operation                RangeInspectOperation
	arity                    uint8
}

func RangeInspectResult(operation RangeInspectOperation, input *Descriptor) *Descriptor {
	if input == nil || input.IsDomain() || !input.Concrete() {
		return nil
	}
	r := input
	if input.IsMultirange() {
		if !input.validMultirangeAuthority() {
			return nil
		}
		r = input.multirangeRange
	}
	if !r.IsRange() || r.rangeSubtype == nil || !r.rangeSubtype.Concrete() {
		return nil
	}
	switch operation {
	case RangeConstruct:
		if !input.IsRange() || input.multirangeCompanion == nil || !input.multirangeCompanion.validMultirangeAuthority() {
			return nil
		}
		return input.multirangeCompanion
	case RangeLower, RangeUpper:
		return r.rangeSubtype
	case RangeIsEmpty, RangeLowerInclusive, RangeUpperInclusive, RangeLowerInfinite, RangeUpperInfinite:
		if r.rangeBoolean == nil || r.rangeBoolean.id != Bool || !r.rangeBoolean.Concrete() {
			return nil
		}
		return r.rangeBoolean
	case RangeMerge:
		return r
	}
	return nil
}
func BindRangeInspect(operation RangeInspectOperation, result Usage, args ...Usage) (RangeInspectPlan, error) {
	var p RangeInspectPlan
	if len(args) < 1 || len(args) > 2 {
		return p, fmt.Errorf("invalid range inspection arity")
	}
	input := args[0].Type
	expected := RangeInspectResult(operation, input)
	if expected == nil || result != expected.Usage() {
		return p, fmt.Errorf("invalid range inspection result authority")
	}
	arity := 1
	if operation == RangeMerge && input.IsRange() {
		arity = 2
	}
	if len(args) != arity {
		return p, fmt.Errorf("invalid range inspection signature")
	}
	for _, arg := range args {
		if arg != input.Usage() {
			return p, fmt.Errorf("foreign range inspection argument")
		}
	}
	r := input
	if input.IsMultirange() {
		r = input.multirangeRange
	}
	return RangeInspectPlan{input, expected, r, operation, uint8(arity)}, nil
}
func (p RangeInspectPlan) Name() string                     { return p.operation.Name() }
func (p RangeInspectPlan) Operation() RangeInspectOperation { return p.operation }
func (p RangeInspectPlan) ResultUsage() Usage               { return p.result.Usage() }
func (p RangeInspectPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if ctx == nil {
		return Value{}, fmt.Errorf("missing range inspection context")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || p.result == nil || p.arity == 0 {
		return Value{}, fmt.Errorf("unbound range inspection plan")
	}
	null := false
	for i := range int(p.arity) {
		if values[i].typ != p.input {
			return Value{}, fmt.Errorf("foreign range inspection value")
		}
		null = null || values[i].null
	}
	if null {
		return Null(p.result), nil
	}
	if p.operation == RangeConstruct {
		member := values[0].rangeDatum
		if !inspectRangeFlags(&member) {
			return Value{}, fmt.Errorf("invalid constructor range flags")
		}
		return constructMultirange(p.result, member), nil
	}
	// Borrow only the touched datums. No endpoint/member copies precede the
	// construction of the final scalar or enclosing range.
	first, last := &values[0].rangeDatum, &values[0].rangeDatum
	if p.input.IsMultirange() {
		payload := values[0].multirange
		if payload == nil {
			return Value{}, fmt.Errorf("missing multirange inspection payload")
		}
		if len(payload.members) == 0 {
			emptyDatum := rangeDatum{flags: rangeEmpty}
			first, last = &emptyDatum, &emptyDatum
		} else {
			first, last = &payload.members[0], &payload.members[len(payload.members)-1]
			if first.flags&rangeEmpty != 0 || last.flags&rangeEmpty != 0 {
				return Value{}, fmt.Errorf("empty multirange inspection member")
			}
		}
	} else if p.arity == 2 {
		last = &values[1].rangeDatum
	}
	if !inspectRangeFlags(first) || last != first && !inspectRangeFlags(last) {
		return Value{}, fmt.Errorf("invalid range inspection flags")
	}
	empty := first.flags&rangeEmpty != 0
	if p.operation == RangeMerge {
		if p.arity == 2 {
			if empty {
				return Value{typ: p.result, rangeDatum: *last}, nil
			}
			if last.flags&rangeEmpty != 0 {
				return Value{typ: p.result, rangeDatum: *first}, nil
			}
			lower, upper := first, first
			lo, err := p.rangeType.compareRangeEndpoint(ctx, *first, true, *last, true)
			if err != nil {
				return Value{}, err
			}
			hi, err := p.rangeType.compareRangeEndpoint(ctx, *first, false, *last, false)
			if err != nil {
				return Value{}, err
			}
			if lo >= 0 {
				lower = last
			}
			if hi <= 0 {
				upper = last
			}
			first, last = lower, upper
		} else if empty {
			return Value{typ: p.result, rangeDatum: rangeDatum{flags: rangeEmpty}}, nil
		}
		return Value{typ: p.result, rangeDatum: rangeDatum{
			lower: first.lower, upper: last.upper,
			flags: first.flags&(rangeLowerInclusive|rangeLowerUnbounded) | last.flags&(rangeUpperInclusive|rangeUpperUnbounded),
		}}, nil
	}
	switch p.operation {
	case RangeLower, RangeUpper:
		bound, absent := &first.lower, first.flags&rangeLowerUnbounded != 0
		if p.operation == RangeUpper {
			bound, absent = &last.upper, last.flags&rangeUpperUnbounded != 0
		}
		if empty || absent {
			return Null(p.result), nil
		}
		if p.rangeType.customRange != nil {
			if bound.value == nil || bound.value.typ != p.result {
				return Value{}, fmt.Errorf("invalid custom range endpoint")
			}
			return *bound.value, nil
		}
		if p.result.id == Numeric {
			return bound.numericValue(p.result), nil
		}
		return Value{typ: p.result, integer: bound.magnitude}, nil
	case RangeIsEmpty:
		return Boolean(p.result, empty), nil
	case RangeLowerInclusive:
		return Boolean(p.result, !empty && first.flags&rangeLowerInclusive != 0), nil
	case RangeUpperInclusive:
		return Boolean(p.result, !empty && last.flags&rangeUpperInclusive != 0), nil
	case RangeLowerInfinite:
		return Boolean(p.result, !empty && first.flags&rangeLowerUnbounded != 0), nil
	case RangeUpperInfinite:
		return Boolean(p.result, !empty && last.flags&rangeUpperUnbounded != 0), nil
	}
	return Value{}, fmt.Errorf("invalid range inspection operation")
}

// The codecs seal and validate endpoint payloads at publication. Checking scalar
// flags does not rescan numeric coefficients or the normalized member sequence.
func inspectRangeFlags(r *rangeDatum) bool {
	if r.flags&rangeEmpty != 0 {
		return *r == (rangeDatum{flags: rangeEmpty})
	}
	if r.flags&^(rangeLowerInclusive|rangeUpperInclusive|rangeLowerUnbounded|rangeUpperUnbounded) != 0 {
		return false
	}
	return (r.flags&rangeLowerUnbounded == 0 || (r.lower == (rangeBound{}) && r.flags&rangeLowerInclusive == 0)) && (r.flags&rangeUpperUnbounded == 0 || (r.upper == (rangeBound{}) && r.flags&rangeUpperInclusive == 0))
}
