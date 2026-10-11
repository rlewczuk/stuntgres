package sqltype

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqlstate"
)

// Storage is owned by the codec and never mutated after publication. Descriptor
// authority belongs only to the outer Value, allowing generation-only Retag.
type multirangePayload struct{ members []rangeDatum }

func isMultirange(id ID) bool {
	return id == Int4Multirange || id == Int8Multirange || id == NumMultirange
}

func (d *Descriptor) validMultirangeAuthority() bool {
	return d != nil && isMultirange(d.id) && d.multirangeRange != nil && isRange(d.multirangeRange.id) && d.multirangeRange.Concrete() && d.multirangeRange.multirangeCompanion == d && d.multirangeRange.rangeSubtype.Concrete()
}

func malformedMultirange(input, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed multirange literal: "` + input + `"`, Detail: detail}
}

func (d *Descriptor) multirangeInput(ctx context.Context, input string) (Value, error) {
	r := d.multirangeRange
	if !d.validMultirangeAuthority() {
		return Value{}, fmt.Errorf("missing multirange range authority")
	}
	at := 0
	for at < len(input) && space(input[at]) {
		at++
	}
	if at == len(input) || input[at] != '{' {
		return Value{}, malformedMultirange(input, "Missing left brace.")
	}
	at++
	const (
		before = iota
		inRange
		escaped
		quoted
		quotedEscaped
		after
		finished
	)
	state, start, seen := before, 0, false
	var members []rangeDatum
	nextCheck := at
	for state != finished {
		if at >= nextCheck {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			nextCheck = at + 1024
		}
		if at == len(input) {
			return Value{}, malformedMultirange(input, "Unexpected end of input.")
		}
		ch := input[at]
		// PostgreSQL skips C whitespace in every scanner state, including
		// escaped states. The member substring still retains those bytes.
		if space(ch) {
			at++
			continue
		}
		switch state {
		case before:
			if ch == '[' || ch == '(' {
				start, state = at, inRange
			} else if ch == '}' && !seen {
				state = finished
			} else if len(input)-at >= 5 && strings.EqualFold(input[at:at+5], "empty") {
				seen, state = true, after
				at += 4
			} else {
				return Value{}, malformedMultirange(input, "Expected range start.")
			}
		case inRange:
			if ch == ']' || ch == ')' {
				// Decode immediately: completed bad members precede suffix errors.
				value, err := r.rangeInput(ctx, input[start:at+1])
				if err != nil {
					return Value{}, err
				}
				if value.rangeDatum.flags != rangeEmpty {
					members = append(members, value.rangeDatum)
				}
				seen, state = true, after
			} else if ch == '"' {
				state = quoted
			} else if ch == '\\' {
				state = escaped
			}
		case escaped:
			state = inRange
		case quoted:
			if ch == '"' {
				if at+1 < len(input) && input[at+1] == '"' {
					at++
				} else {
					state = inRange
				}
			} else if ch == '\\' {
				state = quotedEscaped
			}
		case quotedEscaped:
			state = quoted
		case after:
			if ch == ',' {
				state = before
			} else if ch == '}' {
				state = finished
			} else {
				return Value{}, malformedMultirange(input, "Expected comma or end of multirange.")
			}
		}
		at++
	}
	for at < len(input) && space(input[at]) {
		at++
	}
	if at != len(input) {
		return Value{}, malformedMultirange(input, "Junk after closing right brace.")
	}
	members, err := r.normalizeRangeMembers(ctx, members)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: d, multirange: &multirangePayload{members}}, nil
}

// normalizeRangeMembers consumes writable owned storage. Codecs and aggregate
// finals share the released strict-sort/right-bound tie rules.
func (r *Descriptor) normalizeRangeMembers(ctx context.Context, members []rangeDatum) ([]rangeDatum, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	comparisons := 0
	var failure error
	slices.SortFunc(members, func(a, b rangeDatum) int {
		if comparisons&255 == 0 {
			failure = ctx.Err()
		}
		comparisons++
		if failure != nil {
			return 0
		}
		return r.compareRanges(a, b)
	})
	if failure != nil {
		return nil, failure
	}
	used := 0
	for i, member := range members {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if used == 0 || r.rangesSeparated(members[used-1], member) {
			members[used] = member
			used++
		} else {
			last := &members[used-1]
			// Released range_union chooses the second bound on equality.
			if r.compareRangeEndpoint(*last, true, member, true) >= 0 {
				last.lower = member.lower
				last.flags = last.flags&^(rangeLowerInclusive|rangeLowerUnbounded) | member.flags&(rangeLowerInclusive|rangeLowerUnbounded)
			}
			if r.compareRangeEndpoint(*last, false, member, false) <= 0 {
				last.upper = member.upper
				last.flags = last.flags&^(rangeUpperInclusive|rangeUpperUnbounded) | member.flags&(rangeUpperInclusive|rangeUpperUnbounded)
			}
		}
	}
	// Clear discarded bound strings without copying or shrinking owned storage.
	clear(members[used:])
	return members[:used], ctx.Err()
}

// Endpoint order includes infinity direction, inclusivity and lower/upper side.
func (d *Descriptor) compareRangeEndpoint(a rangeDatum, lowerA bool, b rangeDatum, lowerB bool) int {
	boundA, absentA, inclusiveA := a.upper, a.flags&rangeUpperUnbounded != 0, a.flags&rangeUpperInclusive != 0
	boundB, absentB, inclusiveB := b.upper, b.flags&rangeUpperUnbounded != 0, b.flags&rangeUpperInclusive != 0
	if lowerA {
		boundA, absentA, inclusiveA = a.lower, a.flags&rangeLowerUnbounded != 0, a.flags&rangeLowerInclusive != 0
	}
	if lowerB {
		boundB, absentB, inclusiveB = b.lower, b.flags&rangeLowerUnbounded != 0, b.flags&rangeLowerInclusive != 0
	}
	if absentA && absentB {
		if lowerA == lowerB {
			return 0
		}
		if lowerA {
			return -1
		}
		return 1
	}
	if absentA {
		if lowerA {
			return -1
		}
		return 1
	}
	if absentB {
		if lowerB {
			return 1
		}
		return -1
	}
	if cmp := d.compareRangeBounds(boundA, boundB); cmp != 0 {
		return cmp
	}
	if !inclusiveA && !inclusiveB {
		if lowerA == lowerB {
			return 0
		}
		if lowerA {
			return 1
		}
		return -1
	}
	if !inclusiveA {
		if lowerA {
			return 1
		}
		return -1
	}
	if !inclusiveB {
		if lowerB {
			return -1
		}
		return 1
	}
	return 0
}

func (d *Descriptor) compareRanges(a, b rangeDatum) int {
	if cmp := d.compareRangeEndpoint(a, true, b, true); cmp != 0 {
		return cmp
	}
	return d.compareRangeEndpoint(a, false, b, false)
}

func (d *Descriptor) rangesSeparated(a, b rangeDatum) bool {
	if a.flags&rangeUpperUnbounded != 0 || b.flags&rangeLowerUnbounded != 0 {
		return false
	}
	cmp := d.compareRangeBounds(a.upper, b.lower)
	return cmp < 0 || cmp == 0 && a.flags&rangeUpperInclusive == 0 && b.flags&rangeLowerInclusive == 0
}

func (v Value) validMultirange() bool {
	if v.typ == nil || v.null || v.multirange == nil {
		return false
	}
	d := v.typ.BaseType()
	r := d.multirangeRange
	if !d.validMultirangeAuthority() {
		return false
	}
	for i, member := range v.multirange.members {
		if member.flags&rangeEmpty != 0 || !r.validRangeDatum(member) {
			return false
		}
		if i > 0 && (r.compareRanges(v.multirange.members[i-1], member) >= 0 || !r.rangesSeparated(v.multirange.members[i-1], member)) {
			return false
		}
	}
	return true
}

func (v Value) multirangeOutput(ctx context.Context) (string, error) {
	if !v.validMultirange() {
		return "", fmt.Errorf("invalid multirange datum")
	}
	var out strings.Builder
	out.WriteByte('{')
	for i, member := range v.multirange.members {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if i != 0 {
			out.WriteByte(',')
		}
		v.typ.BaseType().multirangeRange.writeRangeDatum(&out, member)
	}
	out.WriteByte('}')
	return out.String(), nil
}
