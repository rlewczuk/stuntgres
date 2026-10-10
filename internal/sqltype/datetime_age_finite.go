package sqltype

import (
	"context"
	"fmt"
)

// DatetimeAgeFinitePlan retains provider-owned descriptors, not output settings
// or clocks. Definition remapping rebuilds it against the destination provider.
type DatetimeAgeFinitePlan struct {
	name                string
	input, result, date *Descriptor
	count               int
}

func BindDatetimeAgeFinite(name string, input, result, date *Descriptor, count int) (DatetimeAgeFinitePlan, error) {
	valid := input != nil && result != nil
	if name == "age" {
		valid = valid && input.ID() == Timestamp && result.ID() == Interval && (count == 1 || count == 2) && (count == 2 || date != nil && date.ID() == Date)
	} else if name == "isfinite" {
		valid = valid && result.ID() == Bool && count == 1 && (input.ID() == Date || input.ID() == Timestamp || input.ID() == Interval)
	} else {
		valid = false
	}
	if !valid {
		return DatetimeAgeFinitePlan{}, fmt.Errorf("invalid age/isfinite plan")
	}
	return DatetimeAgeFinitePlan{name, input, result, date, count}, nil
}

func (p DatetimeAgeFinitePlan) Name() string           { return p.name }
func (p DatetimeAgeFinitePlan) InputID() ID            { return p.input.ID() }
func (p DatetimeAgeFinitePlan) SessionDependent() bool { return p.name == "age" && p.count == 1 }
func (p DatetimeAgeFinitePlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeAgeFinitePlan, error) {
	date := p.date
	if date != nil {
		date = retained(date)
	}
	return BindDatetimeAgeFinite(p.name, retained(p.input), retained(p.result), date, p.count)
}

func (p DatetimeAgeFinitePlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || len(args) != p.count {
		return Value{}, fmt.Errorf("invalid age/isfinite arguments")
	}
	for _, arg := range args {
		if arg.Type() != p.input {
			return Value{}, fmt.Errorf("invalid age/isfinite input descriptor")
		}
	}
	for _, arg := range args {
		if arg.IsNull() {
			return Null(p.result), nil
		}
	}
	if p.name == "isfinite" {
		finite := true
		switch p.input.ID() {
		case Date:
			finite = args[0].integer != DateNegativeInfinity && args[0].integer != DatePositiveInfinity
		case Timestamp:
			finite = timestampInfinity(args[0].integer) == 0
		case Interval:
			finite = intervalInfinity(args[0]) == 0
		}
		return Boolean(p.result, finite), nil
	}
	left, right := args[0].integer, int64(0)
	if p.count == 2 {
		right = args[1].integer
	} else {
		date, err := CurrentDateTime(p.date.Usage(), env)
		if err != nil {
			return Value{}, err
		}
		left, right = date.integer*microsPerDay, left
	}
	return symbolicTimestampAge(p.result, left, right)
}

func symbolicTimestampAge(result *Descriptor, left, right int64) (Value, error) {
	li, ri := timestampInfinity(left), timestampInfinity(right)
	if li != 0 || ri != 0 {
		if li == ri {
			return Value{}, failure("22008", "interval out of range")
		}
		if li != 0 {
			return arithmeticIntervalInfinity(result, li)
		}
		return arithmeticIntervalInfinity(result, -ri)
	}
	ld, rd := floorDiv(left, microsPerDay), floorDiv(right, microsPerDay)
	ly, lm, lday := dayCivil(ld)
	ry, rm, rday := dayCivil(rd)
	year, month, day := ly-ry, lm-rm, lday-rday
	// Subtract only within-day microseconds: equivalent to borrowing through
	// fractional seconds/minutes/hours, without overflowing whole timestamps.
	clock := (left - ld*microsPerDay) - (right - rd*microsPerDay)
	negative := left < right
	borrowYear, borrowMonth := ry, rm
	if negative {
		year, month, day, clock = -year, -month, -day, -clock
		borrowYear, borrowMonth = ly, lm
	}
	if clock < 0 {
		clock += microsPerDay
		day--
	}
	for day < 0 {
		day += monthDays(borrowYear, borrowMonth)
		month--
	}
	for month < 0 {
		month += 12
		year--
	}
	months := int32(year*12 + month)
	if negative {
		months, day, clock = -months, -day, -clock
	}
	return IntervalValue(result, months, int32(day), clock)
}
