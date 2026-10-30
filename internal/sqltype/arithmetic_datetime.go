package sqltype

import (
	"context"
	"math"

	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func temporalArithmeticType(id ID) bool {
	return id == Date || id == Time || id == Timetz || id == Timestamp || id == Timestamptz || id == Interval
}

func arithmeticSubtractInt64(a, b int64) (int64, bool) {
	if b < 0 && a > math.MaxInt64+b || b > 0 && a < math.MinInt64+b {
		return 0, false
	}
	return a - b, true
}

func timestampInfinity(n int64) int {
	if n == TimestampNegativeInfinity {
		return -1
	}
	if n == TimestampPositiveInfinity {
		return 1
	}
	return 0
}

func arithmeticTimestamp(d *Descriptor, n int64) (Value, error) {
	if n < timestampMinimum || n >= timestampEnd {
		return Value{}, failure("22008", "timestamp out of range")
	}
	return TimestampValue(d, n)
}

func arithmeticDateMidnight(a Value) (int64, error) {
	n := a.integer
	if timestampInfinity(n) != 0 {
		return n, nil
	}
	if n < timestampMinimum/microsPerDay || n >= timestampEnd/microsPerDay {
		return 0, failure("22008", "date out of range for timestamp")
	}
	return n * microsPerDay, nil
}

func (p Arithmetic) datetimeBinary(ctx context.Context, a, b Value, input InputEnvironment) (Value, error) {
	l, r := a.typ.id, b.typ.id
	// Normalize only released commutations, never subtraction or division.
	if p.operator == ArithmeticAdd && (l == Int4 || l == Time && r == Date || l == Timetz && r == Date || l == Interval && r != Interval) || p.operator == ArithmeticMultiply && l == Float8 {
		a, b = b, a
		l, r = r, l
	}
	if l == Interval {
		if r == Interval {
			return addIntervals(a, b, p.result, p.operator == ArithmeticSubtract)
		}
		return scaleInterval(a, b.floating, p.result, p.operator == ArithmeticDivide)
	}
	switch l {
	case Date:
		if r == Int4 {
			if timestampInfinity(a.integer) != 0 {
				return DateValue(p.result, a.integer)
			}
			delta := b.integer
			if p.operator == ArithmeticSubtract {
				delta = -delta
			}
			return DateValue(p.result, a.integer+delta)
		}
		if r == Date {
			if timestampInfinity(a.integer) != 0 || timestampInfinity(b.integer) != 0 {
				return Value{}, failure("22008", "cannot subtract infinite dates")
			}
			return Integer(p.result, a.integer-b.integer)
		}
		n, err := arithmeticDateMidnight(a)
		if err != nil {
			return Value{}, err
		}
		if r == Time || r == Timetz {
			if timestampInfinity(n) != 0 {
				return TimestampValue(p.result, n)
			}
			n += b.integer
			if r == Timetz {
				n += int64(b.west) * 1000000
			}
			return arithmeticTimestamp(p.result, n)
		}
		return addTimestampInterval(ctx, n, b, p.result, p.operator == ArithmeticSubtract, input)
	case Timestamp, Timestamptz:
		if r == Interval {
			return addTimestampInterval(ctx, a.integer, b, p.result, p.operator == ArithmeticSubtract, input)
		}
		ai, bi := timestampInfinity(a.integer), timestampInfinity(b.integer)
		if ai != 0 || bi != 0 {
			if ai == bi {
				return Value{}, failure("22008", "interval out of range")
			}
			if ai != 0 {
				return arithmeticIntervalInfinity(p.result, ai)
			}
			return arithmeticIntervalInfinity(p.result, -bi)
		}
		u, ok := arithmeticSubtractInt64(a.integer, b.integer)
		if !ok {
			return Value{}, failure("22008", "interval out of range")
		}
		return IntervalValue(p.result, 0, int32(u/microsPerDay), u%microsPerDay)
	case Time, Timetz:
		if r == Time {
			return IntervalValue(p.result, 0, 0, a.integer-b.integer)
		}
		if intervalInfinity(b) != 0 {
			message := "cannot add infinite interval to time"
			if p.operator == ArithmeticSubtract {
				message = "cannot subtract infinite interval from time"
			}
			return Value{}, failure("22008", message)
		}
		// PostgreSQL's clock operators wrap the raw int64 before day reduction.
		// Unlike timestamp and component arithmetic, overflow here is intentional.
		n := a.integer + b.integer
		if p.operator == ArithmeticSubtract {
			n = a.integer - b.integer
		}
		n %= microsPerDay
		if n < 0 {
			n += microsPerDay
		}
		if l == Timetz {
			value, err := TimeValue(p.result, n)
			value.west = a.west // Retain an already valid converted fixed offset.
			return value, err
		}
		return TimeValue(p.result, n)
	}
	panic("unreachable validated datetime signature")
}

func addTimestampInterval(ctx context.Context, n int64, interval Value, d *Descriptor, subtract bool, input InputEnvironment) (Value, error) {
	if err := util.ExecutionCheckpoint(ctx, "datetime.add_interval"); err != nil {
		return Value{}, err
	}
	if subtract {
		var err error
		interval, err = negateInterval(interval, interval.typ)
		if err != nil {
			return Value{}, err
		}
	}
	ti, ii := timestampInfinity(n), intervalInfinity(interval)
	if ti != 0 || ii != 0 {
		if ti != 0 && ii != 0 && ti != ii {
			return Value{}, failure("22008", "timestamp out of range")
		}
		if ti != 0 {
			return TimestampValue(d, n)
		}
		if ii < 0 {
			return TimestampValue(d, TimestampNegativeInfinity)
		}
		return TimestampValue(d, TimestampPositiveInfinity)
	}
	for stage, delta := range [...]int32{interval.months, interval.days} {
		if delta == 0 {
			continue
		}
		if err := workadmission.Admit(ctx, workadmission.WorkCalendarArithmetic); err != nil {
			return Value{}, err
		}
		local := n
		if d.id == Timestamptz {
			east, _ := input.Zone.OffsetAt(floorDiv(n, 1000000) + 946684800)
			local += int64(east) * 1000000
		}
		day := floorDiv(local, microsPerDay)
		clock := local - day*microsPerDay
		if stage == 0 {
			y, m, dom := dayCivil(day)
			month := int64(y)*12 + int64(m) - 1 + int64(delta)
			yy := floorDiv(month, 12)
			mm := month - yy*12 + 1
			if yy < -4713 || yy > 294277 {
				return Value{}, failure("22008", "timestamp out of range")
			}
			if last := monthDays(int(yy), int(mm)); dom > last {
				dom = last
			}
			day = civilDay(int(yy), int(mm), dom)
		} else {
			day += int64(delta)
		}
		// A zoned day stage permits local Julian -1 before rotating to the
		// destination instant. Plain timestamps and month bounds are unchanged.
		minimumDay := int64(dateMinimum)
		if stage == 1 && d.id == Timestamptz {
			minimumDay--
		}
		if day < minimumDay || day > dateMaximum || day < math.MinInt64/microsPerDay || day > math.MaxInt64/microsPerDay {
			return Value{}, failure("22008", "timestamp out of range")
		}
		var ok bool
		local, ok = intervalAdd(day*microsPerDay, clock)
		if !ok {
			return Value{}, failure("22008", "timestamp out of range")
		}
		if d.id == Timestamptz {
			east := input.Zone.CivilOffset(floorDiv(local, 1000000) + 946684800)
			local, ok = arithmeticSubtractInt64(local, int64(east)*1000000)
			if !ok {
				return Value{}, failure("22008", "timestamp out of range")
			}
		}
		if local < timestampMinimum || local >= timestampEnd {
			return Value{}, failure("22008", "timestamp out of range")
		}
		n = local
	}
	n, ok := intervalAdd(n, interval.integer)
	if !ok {
		return Value{}, failure("22008", "timestamp out of range")
	}
	return arithmeticTimestamp(d, n)
}
