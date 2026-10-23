package sqltype

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/sqlstate"
)

// Storage and custom endpoint pairs are immutable after publication. The outer
// descriptor and each typed endpoint retain their own generation authority.
type multirangePayload struct{ members []rangeDatum }

func (d *Descriptor) validMultirangeAuthority() bool {
	return d != nil && d.IsMultirange() && d.multirangeRange.IsRange() && d.multirangeRange.Concrete() && d.multirangeRange.multirangeCompanion == d && d.multirangeRange.rangeSubtype.Concrete()
}

func malformedMultirange(input, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed multirange literal: "` + input + `"`, Detail: detail}
}

func (d *Descriptor) multirangeInput(ctx context.Context, input string, env InputEnvironment) (Value, error) {
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
				value, err := r.rangeInput(ctx, input[start:at+1], env)
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
		if failure != nil {
			return 0
		}
		if comparisons&255 == 0 {
			failure = ctx.Err()
		}
		comparisons++
		if failure != nil {
			return 0
		}
		var cmp int
		cmp, failure = r.compareRanges(ctx, a, b)
		return cmp
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
		separated := used == 0
		if used > 0 {
			var err error
			separated, err = r.rangesSeparated(ctx, members[used-1], member)
			if err != nil {
				return nil, err
			}
		}
		if separated {
			members[used] = member
			used++
		} else {
			last := &members[used-1]
			lo, err := r.compareRangeEndpoint(ctx, *last, true, member, true)
			if err != nil {
				return nil, err
			}
			hi, err := r.compareRangeEndpoint(ctx, *last, false, member, false)
			if err != nil {
				return nil, err
			}
			if lo >= 0 {
				last.lower = member.lower
				last.flags = last.flags&^(rangeLowerInclusive|rangeLowerUnbounded) | member.flags&(rangeLowerInclusive|rangeLowerUnbounded)
			}
			if hi <= 0 {
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
func (d *Descriptor) compareRangeEndpoint(ctx context.Context, a rangeDatum, lowerA bool, b rangeDatum, lowerB bool) (int, error) {
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
			return 0, nil
		}
		if lowerA {
			return -1, nil
		}
		return 1, nil
	}
	if absentA {
		if lowerA {
			return -1, nil
		}
		return 1, nil
	}
	if absentB {
		if lowerB {
			return 1, nil
		}
		return -1, nil
	}
	cmp, err := d.compareRangeBounds(ctx, boundA, boundB)
	if err != nil || cmp != 0 {
		return cmp, err
	}
	if !inclusiveA && !inclusiveB {
		if lowerA == lowerB {
			return 0, nil
		}
		if lowerA {
			return 1, nil
		}
		return -1, nil
	}
	if !inclusiveA {
		if lowerA {
			return 1, nil
		}
		return -1, nil
	}
	if !inclusiveB {
		if lowerB {
			return -1, nil
		}
		return 1, nil
	}
	return 0, nil
}

func (d *Descriptor) compareRanges(ctx context.Context, a, b rangeDatum) (int, error) {
	cmp, err := d.compareRangeEndpoint(ctx, a, true, b, true)
	if err != nil || cmp != 0 {
		return cmp, err
	}
	return d.compareRangeEndpoint(ctx, a, false, b, false)
}

func (d *Descriptor) rangesSeparated(ctx context.Context, a, b rangeDatum) (bool, error) {
	if a.flags&rangeUpperUnbounded != 0 || b.flags&rangeLowerUnbounded != 0 {
		return false, nil
	}
	cmp, err := d.compareRangeBounds(ctx, a.upper, b.lower)
	return cmp < 0 || cmp == 0 && a.flags&rangeUpperInclusive == 0 && b.flags&rangeLowerInclusive == 0, err
}

// validMultirangeShape checks physical generations only, without executing a
// subtype provider. Structural comparison and catalog import run under locks;
// destination ordering/normalization is certified separately before publication.
func (v Value) validMultirangeShape() bool {
	if v.typ == nil || v.null || v.multirange == nil {
		return false
	}
	d := v.typ.BaseType()
	if !d.validMultirangeAuthority() {
		return false
	}
	for _, member := range v.multirange.members {
		if member.flags&rangeEmpty != 0 || !d.RangeType().validRangeDatum(member) {
			return false
		}
	}
	return true
}

func (v Value) validMultirange() bool { return v.validateMultirange(context.Background()) == nil }

func (v Value) validateMultirange(ctx context.Context) error {
	if v.typ == nil || v.null || v.multirange == nil {
		return fmt.Errorf("invalid multirange datum")
	}
	d := v.typ.BaseType()
	r := d.multirangeRange
	if !d.validMultirangeAuthority() {
		return fmt.Errorf("invalid multirange authority")
	}
	for i, member := range v.multirange.members {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if member.flags&rangeEmpty != 0 || !r.validRangeDatum(member) {
			return fmt.Errorf("invalid multirange member")
		}
		if i > 0 {
			cmp, err := r.compareRanges(ctx, v.multirange.members[i-1], member)
			if err != nil {
				return err
			}
			if cmp >= 0 {
				return fmt.Errorf("unordered multirange member")
			}
			separated, err := r.rangesSeparated(ctx, v.multirange.members[i-1], member)
			if err != nil {
				return err
			}
			if !separated {
				return fmt.Errorf("unnormalized multirange member")
			}
		}
	}
	return ctx.Err()
}

func (v Value) multirangeOutput(ctx context.Context, env OutputEnvironment) (string, error) {
	if err := v.validateMultirange(ctx); err != nil {
		return "", err
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
		if err := v.typ.BaseType().multirangeRange.appendRangeDatum(ctx, &out, member, env); err != nil {
			return "", err
		}
	}
	out.WriteByte('}')
	return out.String(), nil
}
