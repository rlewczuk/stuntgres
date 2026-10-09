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
	if !ok || m < math.MinInt32 || m > math.MaxInt32 || d < math.MinInt32 || d > math.MaxInt32 || intervalAvgReserved(int32(m), int32(d), u) {
		return failure("22008", "interval out of range")
	}
	// Publish only a complete successful transition, even after infinity.
	s.months, s.days, s.time = int32(m), int32(d), u
	s.count++
	return nil
}

func intervalAvgReserved(months, days int32, micros int64) bool {
	return months == math.MinInt32 && days == math.MinInt32 && micros == math.MinInt64 || months == math.MaxInt32 && days == math.MaxInt32 && micros == math.MaxInt64
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
	divisor := float64(s.count)
	qm, qd := float64(s.months)/divisor, float64(s.days)/divisor
	if qm < -2147483648.0 || qm >= 2147483648.0 || qd < -2147483648.0 || qd >= 2147483648.0 {
		return Value{}, failure("22008", "interval out of range")
	}
	months, days := int32(qm), int32(qd)
	// PostgreSQL interval_div rounds fractional month/day stages to six
	// decimal places before cascading them. Preserve this operation order.
	monthRemainder := math.RoundToEven((qm-float64(months))*30*1e6) / 1e6
	secondRemainder := math.RoundToEven((qd-float64(days)+monthRemainder-math.Trunc(monthRemainder))*86400*1e6) / 1e6
	daySum := int64(days)
	if math.Abs(secondRemainder) >= 86400 {
		carry := int64(secondRemainder / 86400)
		daySum += carry
		if daySum < math.MinInt32 || daySum > math.MaxInt32 {
			return Value{}, failure("22008", "interval out of range")
		}
		secondRemainder -= float64(carry) * 86400
	}
	daySum += int64(monthRemainder)
	if daySum < math.MinInt32 || daySum > math.MaxInt32 {
		return Value{}, failure("22008", "interval out of range")
	}
	// Explicit rounding of the product excludes a fused multiply-add, matching
	// the released float8 division rather than exact rational averaging.
	fractionMicros := float64(secondRemainder * 1e6)
	micros := math.RoundToEven(float64(s.time)/divisor + fractionMicros)
	if micros < -9223372036854775808.0 || micros >= 9223372036854775808.0 {
		return Value{}, failure("22008", "interval out of range")
	}
	if intervalAvgReserved(months, int32(daySum), int64(micros)) {
		return Value{}, failure("22008", "interval out of range")
	}
	return IntervalValue(d, months, int32(daySum), int64(micros))
}
