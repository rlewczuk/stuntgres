package sqltype

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// TimestampSeriesCursor owns one invocation in constant space. Returned values
// are immutable; Close retains the descriptor for typed EOF.
type TimestampSeriesCursor struct {
	current, stop int64
	step          Value
	typ           *Descriptor
	direction     int
	exhausted     bool
	zone          *Zone
	east          int32
	fixed         bool
}

// OpenTimestampSeries takes already coerced plain timestamps and an interval.
// Strict NULL handling suppresses provider validation, not argument evaluation.
func OpenTimestampSeries(ctx context.Context, start, stop, step Value) (TimestampSeriesCursor, error) {
	return openTimestampSeries(ctx, start, stop, step, Timestamp, InputEnvironment{}, nil)
}

// OpenTimestamptzSeries captures the invocation's calendar zone, independently
// of the statement's output zone. A fourth formal is a separate strict operand.
func OpenTimestamptzSeries(ctx context.Context, start, stop, step Value, input InputEnvironment, timezone ...Value) (TimestampSeriesCursor, error) {
	return openTimestampSeries(ctx, start, stop, step, Timestamptz, input, timezone)
}

func openTimestampSeries(ctx context.Context, start, stop, step Value, id ID, input InputEnvironment, timezone []Value) (TimestampSeriesCursor, error) {
	if err := ctx.Err(); err != nil {
		return TimestampSeriesCursor{}, err
	}
	if start.typ == nil || start.typ.id != id || stop.typ != start.typ || step.typ == nil || step.typ.id != Interval || len(timezone) > 1 {
		return TimestampSeriesCursor{}, fmt.Errorf("invalid timestamp series arguments")
	}
	if len(timezone) == 1 && (timezone[0].typ == nil || timezone[0].typ.id != Text) {
		return TimestampSeriesCursor{}, fmt.Errorf("invalid timestamptz series timezone")
	}
	s := TimestampSeriesCursor{typ: start.typ, current: start.integer, stop: stop.integer, step: step}
	if start.null || stop.null || step.null || len(timezone) == 1 && timezone[0].null {
		s.Close()
		return s, nil
	}
	if id == Timestamptz {
		s.zone = input.Zone
		if len(timezone) == 1 {
			name := timezone[0].text
			// PostgreSQL's text_to_cstring_buffer clips at an encoding-safe
			// boundary before lookup, including for otherwise valid POSIX names.
			if len(name) > 255 {
				end := 255
				for end > 0 && !utf8.RuneStart(name[end]) {
					end--
				}
				name = name[:end]
			}
			specification, err := resolveInputZone(name, input)
			if err != nil {
				return TimestampSeriesCursor{}, err
			}
			// A dynamic abbreviation supplies its full owner. Fixed
			// abbreviations keep an inline offset, never a copied self-pointer.
			s.zone, s.east, s.fixed = specification.zone, specification.east, specification.fixed
		}
	}
	if intervalInfinity(step) != 0 {
		return TimestampSeriesCursor{}, failure("22023", "step size cannot be infinite")
	}
	hi, lo := intervalSpan(step)
	switch {
	case hi < 0:
		s.direction = -1
	case hi > 0 || lo != 0:
		s.direction = 1
	default:
		return TimestampSeriesCursor{}, failure("22023", "step size cannot equal zero")
	}
	return s, nil
}

func (s *TimestampSeriesCursor) Next(ctx context.Context) (Value, bool, error) {
	if err := ctx.Err(); err != nil {
		s.Close()
		return Value{}, false, err
	}
	if s.typ == nil {
		return Value{}, false, fmt.Errorf("unopened timestamp series cursor")
	}
	if s.exhausted {
		return Null(s.typ), false, nil
	}
	if s.direction > 0 && s.current > s.stop || s.direction < 0 && s.current < s.stop {
		s.Close()
		return Null(s.typ), false, nil
	}
	value := Value{typ: s.typ, integer: s.current}
	// PostgreSQL advances before emission, even at an inclusive endpoint and
	// under LIMIT 1. Overflow must not expose this invocation's current row.
	input := InputEnvironment{Zone: s.zone}
	var fixedType [1]zoneType
	var fixedZone Zone
	if s.fixed {
		fixedType[0].east = s.east
		fixedZone = Zone{types: fixedType[:], fixed: true}
		input.Zone = &fixedZone
	}
	next, err := addTimestampInterval(ctx, s.current, s.step, s.typ, false, input)
	if err != nil {
		s.Close()
		return Value{}, false, err
	}
	s.current = next.integer
	if err := ctx.Err(); err != nil {
		s.Close()
		return Value{}, false, err
	}
	return value, true, nil
}

func (s *TimestampSeriesCursor) Close() {
	*s = TimestampSeriesCursor{typ: s.typ, exhausted: true}
}
