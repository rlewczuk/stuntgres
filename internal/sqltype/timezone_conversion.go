package sqltype

import "fmt"

const (
	minProviderSecondsWest = -169 * 60 * 60
	maxProviderSecondsWest = 168 * 60 * 60
)

func TimetzValue(d *Descriptor, clock int64, west int32) (Value, error) {
	if d == nil || d.id != Timetz {
		return Value{}, fmt.Errorf("invalid timetz descriptor")
	}
	// Literal displacements have their narrower bound in numericZone. Explicit
	// POSIX standard/DST offsets reach 168h; default DST extends east to 169h.
	if west < minProviderSecondsWest || west > maxProviderSecondsWest {
		return Value{}, failure("22009", "time zone displacement out of range")
	}
	v, err := TimeValue(d, clock)
	v.west = west
	return v, err
}
func (v Value) TimeZoneOffset() (int32, error) {
	if v.typ == nil || v.typ.id != Timetz || v.null {
		return 0, fmt.Errorf("not a timetz value")
	}
	return v.west, nil
}
func temporalConversion(v Value, target Usage, env InputEnvironment) (Value, error) {
	source, destination := v.typ.id, target.Type.id
	n := v.integer
	infinite := n == TimestampNegativeInfinity || n == TimestampPositiveInfinity
	if source == Date && !infinite {
		if n < timestampMinimum/microsPerDay || n >= timestampEnd/microsPerDay {
			return Value{}, failure("22008", "date out of range for timestamp")
		}
		n *= microsPerDay
	}
	east := int32(0)
	if source == Timestamptz && !infinite {
		east, _ = env.Zone.OffsetAt(floorDiv(n, 1000000) + 946684800)
		n += int64(east) * 1000000
	}
	switch destination {
	case Timestamptz:
		if !infinite {
			east = env.Zone.CivilOffset(floorDiv(n, 1000000) + 946684800)
			n -= int64(east) * 1000000
			if n < timestampMinimum || n >= timestampEnd {
				return Value{}, failure("22008", "timestamp out of range")
			}
		}
		return TimestampValue(target.Type, roundTimestamp(n, target.Typmod))
	case Timestamp:
		if !infinite && (n < timestampMinimum || n >= timestampEnd) {
			return Value{}, failure("22008", "timestamp out of range")
		}
		return TimestampValue(target.Type, roundTimestamp(n, target.Typmod))
	case Date:
		if infinite {
			return DateValue(target.Type, n)
		}
		return DateValue(target.Type, floorDiv(n, microsPerDay))
	case Time, Timetz:
		if infinite {
			return Null(target.Type), nil
		}
		if source != Time && source != Timetz {
			n -= floorDiv(n, microsPerDay) * microsPerDay
		}
		result, err := TimeValue(target.Type, roundTimestamp(n, target.Typmod))
		if destination == Timetz {
			if source == Time {
				if env.TransactionStart.IsZero() && !env.Zone.isFixed() {
					return Value{}, fmt.Errorf("missing temporal conversion clock")
				}
				east, _ = env.Zone.OffsetAt(env.TransactionStart.Unix())
			}
			result.west = -east
		}
		return result, err
	}
	return Value{}, fmt.Errorf("invalid datetime conversion")
}

// AtTimeZone implements the three admitted PostgreSQL AT overloads.
func AtTimeZone(v Value, target *Descriptor, name string, local bool, env InputEnvironment) (Value, error) {
	if v.null {
		return Null(target), nil
	}
	specification := zoneSpecification{zone: env.Zone}
	var err error
	if !local {
		specification, err = resolveInputZone(name, env)
		if err != nil {
			return Value{}, err
		}
	}
	return atTimeZone(v, target, specification, env)
}

// AtTimeZoneInterval consumes an already analyzed interval as a fixed offset.
// It shares the existing AT conversion body, not an interval-to-text cast.
func AtTimeZoneInterval(v Value, target *Descriptor, zone Value, env InputEnvironment) (Value, error) {
	if v.null || zone.null {
		return Null(target), nil
	}
	if zone.typ == nil || zone.typ.id != Interval {
		return Value{}, fmt.Errorf("invalid AT TIME ZONE interval operand")
	}
	if intervalInfinity(zone) != 0 || zone.months != 0 || zone.days != 0 {
		text, err := intervalOutput(zone, env.IntervalStyle)
		if err != nil {
			return Value{}, err
		}
		reason := "must not include months or days"
		if intervalInfinity(zone) != 0 {
			reason = "must be finite"
		}
		return Value{}, failure("22023", fmt.Sprintf("interval time zone %q %s", text, reason))
	}
	return atTimeZone(v, target, zoneSpecification{east: int32(zone.integer / 1000000), fixed: true}, env)
}

func atTimeZone(v Value, target *Descriptor, specification zoneSpecification, env InputEnvironment) (Value, error) {
	switch v.typ.id {
	case Timestamp:
		if v.integer == TimestampNegativeInfinity || v.integer == TimestampPositiveInfinity {
			return TimestampValue(target, v.integer)
		}
		east := specification.offsetCivil(floorDiv(v.integer, 1000000) + 946684800)
		n := v.integer - int64(east)*1000000
		if n < timestampMinimum || n >= timestampEnd {
			return Value{}, failure("22008", "timestamp out of range")
		}
		return TimestampValue(target, n)
	case Timestamptz:
		if v.integer == TimestampNegativeInfinity || v.integer == TimestampPositiveInfinity {
			return TimestampValue(target, v.integer)
		}
		east := specification.offsetInstant(floorDiv(v.integer, 1000000) + 946684800)
		n := v.integer + int64(east)*1000000
		if n < timestampMinimum || n >= timestampEnd {
			return Value{}, failure("22008", "timestamp out of range")
		}
		return TimestampValue(target, n)
	case Timetz:
		if env.TransactionStart.IsZero() && !specification.fixed && !specification.zone.isFixed() {
			return Value{}, fmt.Errorf("missing timetz conversion clock")
		}
		east := specification.offsetInstant(env.TransactionStart.Unix())
		clock := (v.integer + (int64(v.west)+int64(east))*1000000) % microsPerDay
		if clock < 0 {
			clock += microsPerDay
		}
		result, err := TimeValue(target, clock)
		result.west = -east
		return result, err
	}
	return Value{}, fmt.Errorf("invalid AT TIME ZONE operand")
}
