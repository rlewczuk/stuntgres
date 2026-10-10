package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// DatetimeTruncPlan retains only provider descriptors, never a session zone.
// TIME conversion belongs to this selected call, not the general cast graph.
type DatetimeTruncPlan struct {
	field, input, result, zone *Descriptor
}

func BindDatetimeTrunc(field, input, result, zone *Descriptor) (DatetimeTruncPlan, error) {
	valid := field != nil && field.ID() == Text && input != nil && result != nil
	if valid {
		valid = input.ID() == result.ID() && (input.ID() == Timestamp || input.ID() == Timestamptz || input.ID() == Interval) || input.ID() == Time && result.ID() == Interval
		valid = valid && (zone == nil || zone.ID() == Text && input.ID() == Timestamptz)
	}
	if !valid {
		return DatetimeTruncPlan{}, fmt.Errorf("invalid datetime truncation plan")
	}
	return DatetimeTruncPlan{field: field, input: input, result: result, zone: zone}, nil
}
func (p DatetimeTruncPlan) InputID() ID { return p.input.ID() }
func (p DatetimeTruncPlan) SessionDependent() bool {
	return p.input.ID() == Timestamptz && p.zone == nil
}
func (p DatetimeTruncPlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeTruncPlan, error) {
	zone := p.zone
	if zone != nil {
		zone = retained(zone)
	}
	return BindDatetimeTrunc(retained(p.field), retained(p.input), retained(p.result), zone)
}
func (p DatetimeTruncPlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	count := 2
	if p.zone != nil {
		count = 3
	}
	if p.result == nil || len(args) != count || args[0].Type() != p.field || args[1].Type() != p.input || count == 3 && args[2].Type() != p.zone {
		return Value{}, fmt.Errorf("invalid datetime truncation arguments")
	}
	for _, arg := range args {
		if arg.IsNull() {
			return Null(p.result), nil
		}
	}
	zone := env.Zone
	if p.zone != nil {
		name := datetimeTruncClip(args[2].text, 255)
		spec, err := resolveInputZone(name, env)
		if err != nil {
			return Value{}, failure("22023", fmt.Sprintf("time zone \"%s\" not recognized", name))
		}
		zone = spec.zone
		if spec.fixed {
			zone = FixedZone(name, spec.east)
		}
	}
	normalized, unit := datetimeTruncUnit(args[0].text)
	v := args[1]
	v.typ = p.result
	if p.input.ID() == Time {
		v.months, v.days = 0, 0
	}
	if unit == "" {
		return Value{}, failure("22023", fmt.Sprintf("unit \"%s\" not recognized for type %s", normalized, p.result.DiagnosticName()))
	}
	if unit == "timezone" || unit == "timezone_hour" || unit == "timezone_minute" || unit == "week" && p.result.ID() == Interval {
		err := failure("0A000", fmt.Sprintf("unit \"%s\" not supported for type %s", normalized, p.result.DiagnosticName()))
		if unit == "week" {
			err.(*sqlstate.Error).Detail = "Months usually have fractional weeks."
		}
		return Value{}, err
	}
	if p.result.ID() == Interval {
		return datetimeTruncInterval(v, unit), nil
	}
	if timestampInfinity(v.integer) != 0 {
		return v, nil
	}
	day := floorDiv(v.integer, microsPerDay)
	clock := v.integer - day*microsPerDay
	east := int32(0)
	if p.result.ID() == Timestamptz {
		east, _ = zone.OffsetAt(floorDiv(v.integer, 1000000) + 946684800)
		clock += int64(east) * 1000000
		shift := floorDiv(clock, microsPerDay)
		day += shift
		clock -= shift * microsPerDay
	}
	large := false
	switch unit {
	case "microseconds":
	case "milliseconds":
		clock = clock / 1000 * 1000
	case "second":
		clock = clock / 1000000 * 1000000
	case "minute":
		clock = clock / 60000000 * 60000000
	case "hour":
		clock = clock / 3600000000 * 3600000000
	default:
		large = true
		clock = 0
		y, m, d := dayCivil(day)
		switch unit {
		case "day":
		case "week":
			day -= ((day+5)%7 + 7) % 7
		case "month":
			d = 1
			day = civilDay(y, m, d)
		case "quarter":
			m = 3*((m-1)/3) + 1
			day = civilDay(y, m, 1)
		case "year":
			day = civilDay(y, 1, 1)
		case "decade":
			if y > 0 {
				y = y / 10 * 10
			} else {
				y = -((8 - (y - 1)) / 10) * 10
			}
			day = civilDay(y, 1, 1)
		case "century":
			if y > 0 {
				y = ((y+99)/100)*100 - 99
			} else {
				y = -((99-(y-1))/100)*100 + 1
			}
			day = civilDay(y, 1, 1)
		case "millennium":
			if y > 0 {
				y = ((y+999)/1000)*1000 - 999
			} else {
				y = -((999-(y-1))/1000)*1000 + 1
			}
			day = civilDay(y, 1, 1)
		}
	}
	if p.result.ID() == Timestamptz {
		if large {
			east = zone.CivilOffset(day*86400 + clock/1000000 + 946684800)
		}
		clock -= int64(east) * 1000000
		shift := floorDiv(clock, microsPerDay)
		day += shift
		clock -= shift * microsPerDay
	}
	// Normalize the clock before checked reconstruction, including source-offset
	// hour truncation inside non-hour DST transitions and near int64 endpoints.
	if day < floorDiv(timestampMinimum, microsPerDay) || day >= timestampEnd/microsPerDay {
		return Value{}, failure("22008", "timestamp out of range")
	}
	n := day*microsPerDay + clock
	if n < timestampMinimum || n >= timestampEnd {
		return Value{}, failure("22008", "timestamp out of range")
	}
	v.integer = n
	return v, nil
}

func datetimeTruncInterval(v Value, unit string) Value {
	if intervalInfinity(v) != 0 {
		return v
	}
	years, remainder := v.months/12, v.months%12
	switch unit {
	case "millennium":
		v.months = years / 1000 * 1000 * 12
		v.days, v.integer = 0, 0
	case "century":
		v.months = years / 100 * 100 * 12
		v.days, v.integer = 0, 0
	case "decade":
		v.months = years / 10 * 10 * 12
		v.days, v.integer = 0, 0
	case "year":
		v.months = years * 12
		v.days, v.integer = 0, 0
	case "quarter":
		v.months = years*12 + remainder/3*3
		v.days, v.integer = 0, 0
	case "month":
		v.days, v.integer = 0, 0
	case "day":
		v.integer = 0
	case "hour":
		v.integer = v.integer / 3600000000 * 3600000000
	case "minute":
		v.integer = v.integer / 60000000 * 60000000
	case "second":
		v.integer = v.integer / 1000000 * 1000000
	case "milliseconds":
		v.integer = v.integer / 1000 * 1000
	}
	return v
}

func datetimeTruncClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func datetimeTruncUnit(original string) (string, string) {
	s := datetimeTruncClip(original, 63)
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			bytes := []byte(s)
			for j := range bytes {
				if bytes[j] >= 'A' && bytes[j] <= 'Z' {
					bytes[j] += 32
				}
			}
			s = string(bytes)
			break
		}
	}
	key := s
	if len(key) > 10 {
		key = key[:10]
	}
	unit := ""
	switch key {
	case "c", "cent", "centuries", "century":
		unit = "century"
	case "d", "day", "days":
		unit = "day"
	case "dec", "decade", "decades", "decs":
		unit = "decade"
	case "h", "hour", "hours", "hr", "hrs":
		unit = "hour"
	case "m", "min", "mins", "minute", "minutes":
		unit = "minute"
	case "mon", "mons", "month", "months":
		unit = "month"
	case "s", "sec", "second", "seconds", "secs":
		unit = "second"
	case "mil", "millennia", "millennium", "mils":
		unit = "millennium"
	case "ms", "msec", "msecond", "mseconds", "msecs", "millisecon":
		unit = "milliseconds"
	case "us", "usec", "usecond", "useconds", "usecs", "microsecon":
		unit = "microseconds"
	case "qtr", "quarter":
		unit = "quarter"
	case "w", "week", "weeks":
		unit = "week"
	case "y", "year", "years", "yr", "yrs":
		unit = "year"
	case "timezone":
		unit = "timezone"
	case "timezone_h":
		unit = "timezone_hour"
	case "timezone_m":
		unit = "timezone_minute"
	}
	return s, unit
}
