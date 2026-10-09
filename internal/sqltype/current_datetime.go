package sqltype

import (
	"fmt"
	"time"
)

// CurrentDateTime derives a SQL current value from the top-level transaction
// instant and this statement's zone. Civil fields precede typmod rounding;
// timestamptz alone rounds the instant. No clock or timezone authority is owned here.
func CurrentDateTime(usage Usage, env InputEnvironment) (Value, error) {
	if usage.Type == nil || !usage.Valid() {
		return Value{}, fmt.Errorf("invalid current datetime usage")
	}
	if env.TransactionStart.IsZero() {
		return Value{}, fmt.Errorf("missing current datetime transaction clock")
	}
	stamp := env.TransactionStart.UTC().Truncate(time.Microsecond)
	civil := stamp
	east := int32(0)
	if usage.Type.id != Timestamptz {
		east, _ = env.Zone.OffsetAt(stamp.Unix())
		civil = stamp.Add(time.Duration(east) * time.Second)
	}
	switch usage.Type.id {
	case Date:
		return DateFromTime(usage.Type, civil)
	case Time, Timetz:
		h, m, s := civil.Clock()
		clock := int64(h)*3600000000 + int64(m)*60000000 + int64(s)*1000000 + int64(civil.Nanosecond()/1000)
		clock = roundTimestamp(clock, usage.Typmod)
		if usage.Type.id == Timetz {
			return TimetzValue(usage.Type, clock, -east)
		}
		return TimeValue(usage.Type, clock)
	case Timestamp, Timestamptz:
		y, m, d := civil.Date()
		h, minute, s := civil.Clock()
		n, err := civilTimestamp(y, int(m), d, h, minute, s, civil.Nanosecond()/1000)
		if err != nil {
			return Value{}, err
		}
		return TimestampValue(usage.Type, roundTimestamp(n, usage.Typmod))
	}
	return Value{}, fmt.Errorf("invalid current datetime type")
}
