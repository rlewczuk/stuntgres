package sqltype

import (
	"fmt"
	"math"
)

// IntervalAvgState owns one group's checked physical component sums. It does
// not normalize months/days/time into a duration or inherit operand typmods.
// Its zero value is an empty aggregate; Result never changes the state.
type IntervalAvgState struct {
	count                              int64
	months, days                       int32
	time                               int64
	positiveInfinity, negativeInfinity bool
}

func (s *IntervalAvgState) Add(value Value) error {
	if s == nil || value.Type() == nil || value.Type().ID() != Interval {
		return fmt.Errorf("invalid interval average datum or state")
	}
	if value.IsNull() {
		return nil
	}
	months, days, micros, err := value.Interval()
	if err != nil {
		return err
	}
	switch intervalInfinity(value) {
	case 1:
		s.positiveInfinity = true
		return nil
	case -1:
		s.negativeInfinity = true
		return nil
	}
	m := int64(s.months) + int64(months)
	d := int64(s.days) + int64(days)
	u, ok := intervalAdd(s.time, micros)
	if !ok || m < math.MinInt32 || m > math.MaxInt32 || d < math.MinInt32 || d > math.MaxInt32 || intervalReserved(int32(m), int32(d), u) {
		return failure("22008", "interval out of range")
	}
	// Publish only a complete successful transition, even after infinity.
	s.months, s.days, s.time = int32(m), int32(d), u
	s.count++
	return nil
}

func (s *IntervalAvgState) Result(d *Descriptor) (Value, error) {
	if s == nil || d == nil || d.ID() != Interval {
		return Value{}, fmt.Errorf("invalid interval average descriptor or state")
	}
	if s.positiveInfinity && s.negativeInfinity {
		return Value{}, failure("22008", "interval out of range")
	}
	if s.positiveInfinity {
		return IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MaxInt64)
	}
	if s.negativeInfinity {
		return IntervalValue(d, math.MinInt32, math.MinInt32, math.MinInt64)
	}
	if s.count == 0 {
		return Null(d), nil
	}
	return scaleFiniteInterval(s.months, s.days, s.time, float64(s.count), d, true)
}
