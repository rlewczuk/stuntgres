package sqltype

import (
	"context"
	"fmt"
)

// TimestampSeriesCursor owns one invocation in constant space. Returned values
// are immutable; Close retains the descriptor for typed EOF.
type TimestampSeriesCursor struct {
	current, stop int64
	step          Value
	typ           *Descriptor
	direction     int
	exhausted     bool
}

// OpenTimestampSeries takes already coerced plain timestamps and an interval.
// Strict NULL handling suppresses provider validation, not argument evaluation.
func OpenTimestampSeries(ctx context.Context, start, stop, step Value) (TimestampSeriesCursor, error) {
	if err := ctx.Err(); err != nil {
		return TimestampSeriesCursor{}, err
	}
	if start.typ == nil || start.typ.id != Timestamp || stop.typ != start.typ || step.typ == nil || step.typ.id != Interval {
		return TimestampSeriesCursor{}, fmt.Errorf("invalid timestamp series arguments")
	}
	s := TimestampSeriesCursor{typ: start.typ, current: start.integer, stop: stop.integer, step: step}
	if start.null || stop.null || step.null {
		s.Close()
		return s, nil
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
	next, err := addTimestampInterval(s.current, s.step, s.typ, false, InputEnvironment{})
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
