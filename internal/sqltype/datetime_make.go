package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"stuntgres/internal/sqlstate"
)

// DatetimeMakePlan owns constructor-specific validation, not text input/typmods.
// Its result descriptor is reminted by the existing definition replay binder.
type DatetimeMakePlan struct {
	name   string
	result *Descriptor
	count  int
}

func BindDatetimeMake(name string, result *Descriptor, count int) (DatetimeMakePlan, error) {
	id, n := ID(0), 0
	switch name {
	case "make_date":
		id, n = Date, 3
	case "make_time":
		id, n = Time, 3
	case "make_timestamp":
		id, n = Timestamp, 6
	case "make_timestamptz":
		id, n = Timestamptz, 6
		if count == 7 {
			n = 7
		}
	case "make_interval":
		id, n = Interval, 7
	}
	if result == nil || id == 0 || result.ID() != id || count != n {
		return DatetimeMakePlan{}, fmt.Errorf("invalid datetime constructor plan")
	}
	return DatetimeMakePlan{name, result, count}, nil
}
func (p DatetimeMakePlan) Name() string           { return p.name }
func (p DatetimeMakePlan) SessionDependent() bool { return p.name == "make_timestamptz" }

func (p DatetimeMakePlan) Evaluate(ctx context.Context, args []Value, env InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if len(args) != p.count || p.result == nil {
		return Value{}, fmt.Errorf("invalid datetime constructor arguments")
	}
	for _, arg := range args {
		if arg.IsNull() {
			return Null(p.result), nil
		}
	}
	var ints [6]int64
	secSlot := p.count - 1
	if p.name == "make_timestamptz" {
		secSlot = 5
	}
	for i := range secSlot {
		n, err := args[i].Integer()
		if err != nil {
			return Value{}, err
		}
		ints[i] = n
	}
	sec := float64(0)
	if p.name != "make_date" {
		var err error
		sec, err = args[secSlot].Float()
		if err != nil {
			return Value{}, err
		}
	} else {
		n, err := args[2].Integer()
		if err != nil {
			return Value{}, err
		}
		ints[2] = n
	}
	switch p.name {
	case "make_interval":
		return makeInterval(p.result, ints, sec)
	case "make_time":
		clock, ok := makeClock(ints[0], ints[1], sec)
		if !ok {
			return Value{}, makeClockError(ints[0], ints[1], sec)
		}
		return TimeValue(p.result, clock)
	}
	originalYear, month, day := ints[0], ints[1], ints[2]
	year := originalYear
	if year < 0 && year != math.MinInt32 {
		year++
	}
	shownYear := originalYear
	if p.name == "make_date" {
		shownYear = year
	}
	if originalYear == 0 || originalYear == math.MinInt32 || month < 1 || month > 12 || day < 1 || day > int64(monthDays(int(year), int(month))) {
		return Value{}, failure("22008", fmt.Sprintf("date field value out of range: %d-%02d-%02d", shownYear, month, day))
	}
	// REL_18_0 IS_VALID_JULIAN admits month-boundary slop before the
	// constructor's own finite check; TIMESTAMP must still check its clock first.
	if year < -4713 || year == -4713 && month < 11 || year > 5874898 || year == 5874898 && month >= 6 {
		return Value{}, failure("22008", fmt.Sprintf("date out of range: %d-%02d-%02d", shownYear, month, day))
	}
	days := civilDay(int(year), int(month), int(day))
	if p.name == "make_date" {
		if days < dateMinimum || days > dateMaximum {
			return Value{}, failure("22008", fmt.Sprintf("date out of range: %d-%02d-%02d", shownYear, month, day))
		}
		return DateValue(p.result, days)
	}
	clock, ok := makeClock(ints[3], ints[4], sec)
	if !ok {
		return Value{}, makeClockError(ints[3], ints[4], sec)
	}
	// Calendar day arithmetic precedes the finite timestamp check. The Julian
	// slop allows 24:00 on the preceding civil day to carry onto finite MIN.
	base, ok := intervalMultiply(days, microsPerDay)
	if !ok {
		return Value{}, makeTimestampRange(originalYear, month, day, ints[3], ints[4], sec)
	}
	n, ok := intervalAdd(base, clock)
	if !ok || n < timestampMinimum || n >= timestampEnd {
		return Value{}, makeTimestampRange(originalYear, month, day, ints[3], ints[4], sec)
	}
	if p.name == "make_timestamptz" {
		specification := zoneSpecification{zone: env.Zone}
		if p.count == 7 {
			name, err := args[6].Text()
			if err != nil {
				return Value{}, err
			}
			specification, err = makeZone(name, env)
			if err != nil {
				return Value{}, err
			}
		}
		// The rounded/carry-normalized civil timestamp determines gap/fold offset.
		east := specification.offsetCivil(floorDiv(n, 1000000) + 946684800)
		rotated, ok := intervalAdd(n, -int64(east)*1000000)
		if !ok || rotated < timestampMinimum || rotated >= timestampEnd {
			return Value{}, failure("22008", "timestamp out of range")
		}
		n = rotated
	}
	return TimestampValue(p.result, n)
}

func makeClock(hour, minute int64, seconds float64) (int64, bool) {
	rounded := math.RoundToEven(seconds * 1000000)
	if math.IsNaN(rounded) || rounded < 0 || rounded > 60000000 || hour < 0 || hour > 24 || minute < 0 || minute > 59 {
		return 0, false
	}
	clock := hour*3600000000 + minute*60000000 + int64(rounded)
	return clock, clock <= microsPerDay
}

// C's %02g uses six significant digits; Go's default %g is shortest instead.
func makeSecondsText(seconds float64) string {
	switch {
	case math.IsNaN(seconds):
		return "NaN"
	case math.IsInf(seconds, 1):
		return "Infinity"
	case math.IsInf(seconds, -1):
		return "-Infinity"
	}
	s := strconv.FormatFloat(seconds, 'g', 6, 64)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}
func makeClockError(hour, minute int64, seconds float64) error {
	return failure("22008", fmt.Sprintf("time field value out of range: %d:%02d:%s", hour, minute, makeSecondsText(seconds)))
}
func makeTimestampRange(year, month, day, hour, minute int64, seconds float64) error {
	return failure("22008", fmt.Sprintf("timestamp out of range: %d-%02d-%02d %d:%02d:%s", year, month, day, hour, minute, makeSecondsText(seconds)))
}

func makeInterval(result *Descriptor, ints [6]int64, seconds float64) (Value, error) {
	months := ints[0] * 12
	days := ints[2] * 7
	invalid := func() (Value, error) { return Value{}, failure("22008", "interval out of range") }
	if months < math.MinInt32 || months > math.MaxInt32 || days < math.MinInt32 || days > math.MaxInt32 {
		return invalid()
	}
	months += ints[1]
	days += ints[3]
	if months < math.MinInt32 || months > math.MaxInt32 || days < math.MinInt32 || days > math.MaxInt32 {
		return invalid()
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return invalid()
	}
	scaled := seconds * 1000000
	if math.IsInf(scaled, 0) {
		return Value{}, failure("22003", "value out of range: overflow")
	}
	rounded := math.RoundToEven(scaled)
	// float64(MaxInt64) equals +2^63: exclude that value BEFORE conversion.
	if rounded < -0x1p63 || rounded >= 0x1p63 {
		return invalid()
	}
	n, ok := intervalAdd(ints[4]*3600000000+ints[5]*60000000, int64(rounded))
	if !ok {
		return invalid()
	}
	v, err := IntervalValue(result, int32(months), int32(days), n)
	if err == nil && intervalInfinity(v) != 0 {
		return invalid()
	}
	return v, err
}

func makeZone(name string, env InputEnvironment) (zoneSpecification, error) {
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		err := failure("22023", fmt.Sprintf("invalid input syntax for type numeric time zone: \"%s\"", name)).(*sqlstate.Error)
		err.Hint = "Numeric time zones must have \"-\" or \"+\" as first character."
		return zoneSpecification{}, err
	}
	if len(name) > 0 && (name[0] == '+' || name[0] == '-') {
		status := numericZone(name)
		if status == timestampDecoded {
			return zoneSpecification{east: numericZoneEast(name), fixed: true}, nil
		}
		if status == timestampZoneOverflow {
			return zoneSpecification{}, failure("22023", fmt.Sprintf("numeric time zone \"%s\" out of range", name))
		}
		// Malformed signed names can still be resolved by the existing zone provider.
	}
	return resolveInputZone(name, env)
}
