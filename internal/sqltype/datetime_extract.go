package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// DatetimeExtractPlan owns descriptors and the DATE wrapper cast. Session state
// is supplied only at evaluation; stored definitions rebuild plans on Retag.
type DatetimeExtractPlan struct {
	name                            string
	field, input, result, timestamp *Descriptor
	dateCast                        CoercionPlan
}

func BindDatetimeExtract(name string, field, input, result, timestamp *Descriptor) (DatetimeExtractPlan, error) {
	valid := field != nil && field.ID() == Text && input != nil && result != nil && (name == "extract" && result.ID() == Numeric || name == "date_part" && result.ID() == Float8)
	if valid {
		switch input.ID() {
		case Date, Time, Timetz, Timestamp, Timestamptz, Interval:
		default:
			valid = false
		}
	}
	if !valid {
		return DatetimeExtractPlan{}, fmt.Errorf("invalid datetime extraction plan")
	}
	p := DatetimeExtractPlan{name: name, field: field, input: input, result: result, timestamp: timestamp}
	if name == "date_part" && input.ID() == Date {
		if timestamp == nil || timestamp.ID() != Timestamp {
			return DatetimeExtractPlan{}, fmt.Errorf("invalid extraction DATE wrapper")
		}
		var err error
		p.dateCast, err = PlanCoercion(input.Usage(), timestamp.Usage(), Explicit)
		if err != nil {
			return DatetimeExtractPlan{}, err
		}
	}
	return p, nil
}
func (p DatetimeExtractPlan) Name() string           { return p.name }
func (p DatetimeExtractPlan) InputID() ID            { return p.input.ID() }
func (p DatetimeExtractPlan) SessionDependent() bool { return p.input.ID() == Timestamptz }
func (p DatetimeExtractPlan) Retag(retained func(*Descriptor) *Descriptor) (DatetimeExtractPlan, error) {
	timestamp := p.timestamp
	if timestamp != nil {
		timestamp = retained(timestamp)
	}
	return BindDatetimeExtract(p.name, retained(p.field), retained(p.input), retained(p.result), timestamp)
}
func (p DatetimeExtractPlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || len(args) != 2 || args[0].Type() != p.field || args[1].Type() != p.input {
		return Value{}, fmt.Errorf("invalid datetime extraction arguments")
	}
	if args[0].IsNull() || args[1].IsNull() {
		return Null(p.result), nil
	}
	v := args[1]
	if p.name == "date_part" && p.input.ID() == Date {
		var err error
		v, err = p.dateCast.Apply(ctx, v, env, OutputEnvironment{})
		if err != nil {
			return Value{}, err
		}
	}
	normalized, unit := datetimeExtractUnit(args[0].text)
	unsupported := func() (Value, error) {
		return Value{}, failure("0A000", fmt.Sprintf("unit \"%s\" not supported for type %s", normalized, v.typ.DiagnosticName()))
	}
	if unit == "" {
		return Value{}, failure("22023", fmt.Sprintf("unit \"%s\" not recognized for type %s", normalized, v.typ.DiagnosticName()))
	}
	infinite := 0
	if v.typ.ID() == Interval {
		infinite = intervalInfinity(v)
	} else if v.typ.ID() != Time && v.typ.ID() != Timetz {
		infinite = timestampInfinity(v.integer)
	}
	if infinite != 0 {
		monotone, nulls := false, false
		if v.typ.ID() == Interval {
			switch unit {
			case "epoch", "hour", "day", "year", "decade", "century", "millennium":
				monotone = true
			case "microseconds", "milliseconds", "second", "minute", "week", "month", "quarter":
				nulls = true
			}
		} else {
			switch unit {
			case "epoch", "julian", "year", "isoyear", "decade", "century", "millennium":
				monotone = true
			default:
				nulls = v.typ.ID() != Date || unit == "day" || unit == "month" || unit == "quarter" || unit == "week" || unit == "dow" || unit == "isodow" || unit == "doy"
			}
		}
		if monotone {
			if p.name == "date_part" {
				return Float(p.result, math.Inf(infinite))
			}
			special := numericInfinity
			if infinite < 0 {
				special = numericNegativeInfinity
			}
			return Value{typ: p.result, special: special}, nil
		}
		if nulls {
			return Null(p.result), nil
		}
		return unsupported()
	}
	if unit == "epoch" {
		return p.epoch(v)
	}
	days, clock, east := int64(0), v.integer, int32(0)
	switch v.typ.ID() {
	case Date:
		days, clock = v.integer, 0
	case Timestamp, Timestamptz:
		// Decompose before adding zone seconds so near-bound instants do not
		// overflow a microsecond int64 while deriving their local civil clock.
		days = floorDiv(v.integer, microsPerDay)
		clock = v.integer - days*microsPerDay
		if v.typ.ID() == Timestamptz {
			east, _ = env.Zone.OffsetAt(floorDiv(v.integer, 1000000) + 946684800)
			clock += int64(east) * 1000000
			shift := floorDiv(clock, microsPerDay)
			days += shift
			clock -= shift * microsPerDay
		}
	case Timetz:
		east = -v.west
	}
	integral := func(n int64) (Value, error) {
		if p.name == "date_part" {
			return Float(p.result, float64(n))
		}
		return datetimeExtractNumeric(p.result, n, 0), nil
	}
	scaled := func(n int64, scale int64) (Value, error) {
		if p.name == "date_part" {
			seconds, fraction := float64(n/1000000), float64(n%1000000)
			if scale == 3 {
				return Float(p.result, seconds*1000+fraction/1000)
			}
			return Float(p.result, seconds+fraction/1000000)
		}
		return datetimeExtractNumeric(p.result, n, scale), nil
	}
	if v.typ.ID() == Interval {
		year, month := int64(v.months/12), int64(v.months%12)
		switch unit {
		case "year":
			return integral(year)
		case "month":
			return integral(month)
		case "century":
			return integral(year / 100)
		case "decade":
			return integral(year / 10)
		case "millennium":
			return integral(year / 1000)
		case "quarter":
			if v.months < 0 {
				return integral(-int64(((-v.months % 12) / 3) + 1))
			}
			return integral(month/3 + 1)
		case "day":
			return integral(int64(v.days))
		case "week":
			return integral(int64(v.days / 7))
		case "hour":
			return integral(clock / 3600000000)
		case "minute":
			return integral(clock / 60000000 % 60)
		case "second":
			return scaled(clock%60000000, 6)
		case "milliseconds":
			return scaled(clock%60000000, 3)
		case "microseconds":
			return integral(clock % 60000000)
		default:
			return unsupported()
		}
	}
	switch unit {
	case "hour", "minute", "second", "milliseconds", "microseconds":
		if v.typ.ID() == Date {
			return unsupported()
		}
		switch unit {
		case "hour":
			return integral(clock / 3600000000)
		case "minute":
			return integral(clock / 60000000 % 60)
		case "second":
			return scaled(clock%60000000, 6)
		case "milliseconds":
			return scaled(clock%60000000, 3)
		default:
			return integral(clock % 60000000)
		}
	case "timezone", "timezone_hour", "timezone_minute":
		if v.typ.ID() != Timetz && v.typ.ID() != Timestamptz {
			return unsupported()
		}
		switch unit {
		case "timezone":
			return integral(int64(east))
		case "timezone_hour":
			return integral(int64(east / 3600))
		default:
			return integral(int64(east / 60 % 60))
		}
	}
	if v.typ.ID() == Time || v.typ.ID() == Timetz {
		return unsupported()
	}
	// These fields depend only on the day count/clock; avoid civil decomposition.
	switch unit {
	case "dow", "isodow":
		dow := (days + 6) % 7
		if dow < 0 {
			dow += 7
		}
		if unit == "isodow" && dow == 0 {
			dow = 7
		}
		return integral(dow)
	case "julian":
		julian := days + 2451545
		if v.typ.ID() == Date {
			return integral(julian)
		}
		if p.name == "date_part" {
			return Float(p.result, float64(julian)+(float64(clock/1000000)+float64(clock%1000000)/1000000)/86400)
		}
		fraction := datetimeExtractDivide(p.result, clock, microsPerDay)
		return datetimeExtractJulian(p.result, julian, fraction), nil
	}
	y, m, d := dayCivil(days)
	switch unit {
	case "year":
		if y <= 0 {
			y--
		}
		return integral(int64(y))
	case "month":
		return integral(int64(m))
	case "day":
		return integral(int64(d))
	case "quarter":
		return integral(int64((m-1)/3 + 1))
	case "century":
		if y > 0 {
			return integral(int64((y + 99) / 100))
		}
		return integral(int64(-((100 - y) / 100)))
	case "millennium":
		if y > 0 {
			return integral(int64((y + 999) / 1000))
		}
		return integral(int64(-((1000 - y) / 1000)))
	case "decade":
		if y >= 0 {
			return integral(int64(y / 10))
		}
		return integral(int64(-((9 - y) / 10)))
	case "doy":
		return integral(days - civilDay(y, 1, 1) + 1)
	case "week", "isoyear":
		iy, iw, _ := datetimeISO(y, m, d)
		if unit == "week" {
			return integral(int64(iw))
		}
		if iy <= 0 {
			iy--
		}
		return integral(int64(iy))
	}
	return unsupported()
}

func datetimeExtractNumeric(d *Descriptor, n, scale int64) Value {
	text := strconv.FormatInt(n, 10)
	negative := n < 0
	if negative {
		text = text[1:]
	}
	return normalizeNumeric(Value{typ: d, text: text, negative: negative, exponent: -scale, scale: scale})
}

func datetimeExtractUnit(original string) (string, string) {
	s := original
	if len(s) > 63 {
		end := 63
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
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
	case "m", "min", "mins", "minute", "minutes", "mm":
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
	case "j", "jd", "julian":
		unit = "julian"
	case "dow", "doy", "isodow", "isoyear", "epoch", "timezone":
		unit = key
	case "timezone_h":
		unit = "timezone_hour"
	case "timezone_m":
		unit = "timezone_minute"
	}
	return s, unit
}
