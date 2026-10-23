package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// RangeFromValues retains immutable typed endpoints, never a text round trip.
// NULL endpoints mean infinity. Only genuine custom families use this entry.
func RangeFromValues(ctx context.Context, d *Descriptor, lower, upper Value, flags string) (Value, error) {
	if d == nil || d.customRange == nil || !d.IsRange() {
		return Value{}, fmt.Errorf("invalid range constructor authority")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if len(flags) != 2 || (flags[0] != '[' && flags[0] != '(') || (flags[1] != ']' && flags[1] != ')') {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid range bound flags", Hint: `Valid values are "[]", "[)", "(]", and "()".`}
	}
	var bits uint8
	if flags[0] == '[' {
		bits |= rangeLowerInclusive
	}
	if flags[1] == ']' {
		bits |= rangeUpperInclusive
	}
	return d.rangeFromTypedBounds(ctx, lower, upper, bits, false)
}

// Codecs and generated calls share validation before allocating their one
// endpoint pair. Empty, unbounded and rejected inputs allocate no pair.
func (d *Descriptor) rangeFromTypedBounds(ctx context.Context, lower, upper Value, flags uint8, inputSave bool) (Value, error) {
	if lower.Type() != d.rangeSubtype || upper.Type() != d.rangeSubtype {
		return Value{}, fmt.Errorf("range constructor subtype generation mismatch")
	}
	if lower.IsNull() {
		flags = flags&^rangeLowerInclusive | rangeLowerUnbounded
	}
	if upper.IsNull() {
		flags = flags&^rangeUpperInclusive | rangeUpperUnbounded
	}
	flags, err := d.canonicalGenericBounds(ctx, flags, lower, upper, inputSave)
	if err != nil {
		return Value{}, err
	}
	x := rangeDatum{flags: flags}
	if flags&rangeEmpty == 0 && (!lower.IsNull() || !upper.IsNull()) {
		pair := &rangeEndpointPair{}
		if !lower.IsNull() {
			pair.lower = lower
			x.lower.value = &pair.lower
		}
		if !upper.IsNull() {
			pair.upper = upper
			x.upper.value = &pair.upper
		}
	}
	return Value{typ: d, rangeDatum: x}, nil
}

// MultirangeFromValues owns its normalization buffer, retaining immutable bounds.
func MultirangeFromValues(ctx context.Context, d *Descriptor, members []Value) (Value, error) {
	return MultirangeFromSource(ctx, d, len(members), func(i int) (Value, error) { return members[i], nil })
}

// MultirangeFromSource reads each member synchronously into its one owned
// normalization buffer. The value source is not retained and never supplies
// ordering authority; evaluation can avoid an intermediate []Value allocation.
func MultirangeFromSource(ctx context.Context, d *Descriptor, count int, member func(int) (Value, error)) (Value, error) {
	if d == nil || !d.validMultirangeAuthority() || d.multirangeRange.customRange == nil || count < 0 || member == nil {
		return Value{}, fmt.Errorf("invalid multirange constructor authority")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var data []rangeDatum
	var memberError error
	for i := range count {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		v, err := member(i)
		if err != nil {
			return Value{}, err
		}
		// SQL evaluates the complete argument array before invoking its
		// constructor. A child evaluation error precedes a NULL-member error.
		if memberError != nil {
			continue
		}
		if v.IsNull() {
			memberError = failure("22004", "multirange values cannot contain null members")
			continue
		}
		if v.Type() != d.multirangeRange || !v.validRange() {
			return Value{}, fmt.Errorf("multirange constructor member generation mismatch")
		}
		if v.rangeDatum.flags != rangeEmpty {
			if data == nil {
				data = make([]rangeDatum, 0, count-i)
			}
			data = append(data, v.rangeDatum)
		}
	}
	if memberError != nil {
		return Value{}, memberError
	}
	data, err := d.multirangeRange.normalizeRangeMembers(ctx, data)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: d, multirange: &multirangePayload{members: data}}, nil
}

// MultirangeFromArray borrows the immutable array's leaf slice only during
// construction; the normalized result owns separate member storage.
func MultirangeFromArray(ctx context.Context, d *Descriptor, v Value) (Value, error) {
	if d == nil || !d.validMultirangeAuthority() || d.multirangeRange.customRange == nil || v.Type() == nil || !v.Type().IsArray() || v.Type().Element() != d.RangeType() {
		return Value{}, fmt.Errorf("invalid variadic multirange array")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if v.IsNull() {
		return Null(d), nil
	}
	if v.array == nil {
		return Value{}, fmt.Errorf("missing variadic multirange array payload")
	}
	return MultirangeFromValues(ctx, d, v.array.elements)
}
