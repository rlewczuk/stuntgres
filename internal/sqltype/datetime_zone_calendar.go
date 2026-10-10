package sqltype

import (
	"context"
	"fmt"
)

// CalendarArithmetic reuses the bound calendar stages with a calculation-only
// zone. The temporary fixed handle never escapes the synchronous arithmetic
// operation; named and dynamic abbreviations reuse the existing owning rules.
func CalendarArithmetic(ctx context.Context, p Arithmetic, stamp, interval Value, name string, input InputEnvironment) (Value, error) {
	if p.left == nil || p.right == nil || p.left.id != Timestamptz || p.right.id != Interval || p.result == nil || p.result.id != Timestamptz || p.operator != ArithmeticAdd && p.operator != ArithmeticSubtract || stamp.typ != p.left || interval.typ != p.right {
		return Value{}, fmt.Errorf("invalid calendar arithmetic values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if stamp.null || interval.null {
		return Null(p.result), nil
	}
	// lookup_timezone uses the Default abbreviation set, not a session's
	// abbreviation override; dynamic abbreviations select their full owner.
	specification, err := resolveInputZone(name, InputEnvironment{TimezoneAbbreviations: TimezoneAbbreviationsDefault})
	if err != nil {
		return Value{}, err
	}
	var fixedTypes [1]zoneType
	var fixed Zone
	if specification.fixed {
		fixedTypes[0] = zoneType{east: specification.east, abbreviation: name}
		fixed = Zone{name: name, types: fixedTypes[:], fixed: true}
		input.Zone = &fixed
	} else {
		input.Zone = specification.zone
	}
	// Lookup deliberately precedes zero/infinity/checked interval negation.
	return p.Binary(ctx, stamp, interval, input)
}
