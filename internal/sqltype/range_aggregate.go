package sqltype

import (
	"context"
	"fmt"
	"slices"
)

// RangeAggregateOperation selects a set operation, not a scalar SQL comparator.
type RangeAggregateOperation uint8

const (
	RangeAggregateUnion RangeAggregateOperation = iota
	RangeAggregateIntersection
)

// RangeAggregate holds immutable, same-generation overload authority.
type RangeAggregate struct {
	input, result, rangeType *Descriptor
	operation                RangeAggregateOperation
}

// RangeAggregateState belongs to one execution/group. Published payloads are
// sealed; neither later transitions nor another group's scratch can mutate them.
type RangeAggregateState struct {
	owner               RangeAggregate
	seen, owned, sealed bool
	value               rangeDatum
	members, scratch    []rangeDatum
	final               Value
}

func BindRangeAggregate(input *Descriptor, operation RangeAggregateOperation) (RangeAggregate, error) {
	if input == nil || !input.Concrete() || input.IsDomain() || operation > RangeAggregateIntersection {
		return RangeAggregate{}, fmt.Errorf("invalid range aggregate authority")
	}
	r := input
	if isMultirange(input.id) {
		if !input.validMultirangeAuthority() {
			return RangeAggregate{}, fmt.Errorf("invalid range aggregate family")
		}
		r = input.multirangeRange
	} else if !isRange(input.id) || input.multirangeCompanion == nil ||
		input.multirangeCompanion.multirangeRange != input || !input.multirangeCompanion.validMultirangeAuthority() {
		return RangeAggregate{}, fmt.Errorf("invalid range aggregate family")
	}
	result := input
	if operation == RangeAggregateUnion {
		result = r.multirangeCompanion
	}
	return RangeAggregate{input: input, result: result, rangeType: r, operation: operation}, nil
}

func (p RangeAggregate) ResultUsage() Usage { return p.result.Usage() }

func (p RangeAggregate) Init(ctx context.Context) (*RangeAggregateState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.input == nil || p.result == nil {
		return nil, fmt.Errorf("unbound range aggregate")
	}
	return &RangeAggregateState{owner: p}, nil
}

func (p RangeAggregate) Advance(ctx context.Context, s *RangeAggregateState, v Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.input == nil || s == nil || s.owner != p || s.sealed || v.typ != p.input {
		return fmt.Errorf("invalid range aggregate state or datum")
	}
	if v.null {
		return nil
	}
	if isRange(p.input.id) {
		if !p.rangeType.validRangeDatum(v.rangeDatum) {
			return fmt.Errorf("invalid range aggregate datum")
		}
		if p.operation == RangeAggregateUnion {
			s.seen = true
			if v.rangeDatum.flags&rangeEmpty == 0 {
				s.members = append(s.members, v.rangeDatum)
			}
		} else {
			if !s.seen {
				s.value = v.rangeDatum
			} else {
				s.value = p.rangeType.intersectRangeDatums(s.value, v.rangeDatum)
			}
			s.seen = true
		}
		return ctx.Err()
	}
	if v.multirange == nil {
		return fmt.Errorf("invalid multirange aggregate datum")
	}
	incoming := v.multirange.members
	if p.operation == RangeAggregateUnion {
		s.members = slices.Grow(s.members, len(incoming))
		for len(incoming) != 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(len(incoming), 256)
			s.members = append(s.members, incoming[:n]...)
			incoming = incoming[n:]
		}
		s.seen = true
		return ctx.Err()
	}
	if !s.seen {
		s.members, s.seen = incoming, true // Immutable seed, never used as writable scratch.
		return ctx.Err()
	}
	if len(s.members) == 0 {
		return ctx.Err()
	}
	out := s.scratch[:0]
	for left, right := 0, 0; left < len(s.members) && right < len(incoming); {
		if err := ctx.Err(); err != nil {
			clear(out)
			s.scratch = out[:0]
			return err
		}
		a, b := s.members[left], incoming[right]
		if value := p.rangeType.intersectRangeDatums(a, b); value.flags&rangeEmpty == 0 {
			out = append(out, value)
		}
		cmp := p.rangeType.compareRangeEndpoint(a, false, b, false)
		if cmp <= 0 {
			left++
		}
		if cmp >= 0 {
			right++
		}
	}
	if s.owned {
		clear(s.members)
		s.scratch = s.members[:0]
	} else {
		s.scratch = nil
	}
	s.members, s.owned = out, true
	return ctx.Err()
}

func (p RangeAggregate) Finalize(ctx context.Context, s *RangeAggregateState) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || s == nil || s.owner != p {
		return Value{}, fmt.Errorf("invalid range aggregate state")
	}
	if s.sealed {
		return s.final, nil
	}
	result := Null(p.result)
	if s.seen {
		if isRange(p.result.id) {
			result = Value{typ: p.result, rangeDatum: s.value}
		} else {
			members := s.members
			if p.operation == RangeAggregateUnion {
				var err error
				members, err = p.rangeType.normalizeRangeMembers(ctx, members)
				if err != nil {
					return Value{}, err
				}
			}
			result = Value{typ: p.result, multirange: &multirangePayload{members: members}}
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	clear(s.scratch)
	s.members, s.scratch = nil, nil
	s.final, s.sealed = result, true
	return result, nil
}

func (d *Descriptor) intersectRangeDatums(a, b rangeDatum) rangeDatum {
	if a.flags&rangeEmpty != 0 || b.flags&rangeEmpty != 0 {
		return rangeDatum{flags: rangeEmpty}
	}
	result := a
	if d.compareRangeEndpoint(a, true, b, true) < 0 {
		result.lower = b.lower
		result.flags = result.flags&^(rangeLowerInclusive|rangeLowerUnbounded) | b.flags&(rangeLowerInclusive|rangeLowerUnbounded)
	}
	if d.compareRangeEndpoint(a, false, b, false) > 0 {
		result.upper = b.upper
		result.flags = result.flags&^(rangeUpperInclusive|rangeUpperUnbounded) | b.flags&(rangeUpperInclusive|rangeUpperUnbounded)
	}
	// Bounds are borrowed from valid canonical inputs; endpoint order also
	// distinguishes a closed continuous point from either excluded point.
	if d.compareRangeEndpoint(result, true, result, false) > 0 {
		return rangeDatum{flags: rangeEmpty}
	}
	return result
}
