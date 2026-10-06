package sqltype

import (
	"fmt"
	"math"
	"time"
)

// PostgreSQL DATE is a civil day, not a timestamp or an instant. Julian day
// zero and DATE_END_JULIAN-1 are the inclusive finite endpoints (REL_18_0).
const (
	DateNegativeInfinity int64 = math.MinInt64
	DatePositiveInfinity int64 = math.MaxInt64
	dateMinimum          int64 = -2451545
	dateMaximum          int64 = 2145031948
)

func DateValue(d *Descriptor, days int64) (Value, error) {
	if d == nil || d.id != Date {
		return Value{}, fmt.Errorf("invalid date descriptor")
	}
	if days != DateNegativeInfinity && days != DatePositiveInfinity && (days < dateMinimum || days > dateMaximum) {
		return Value{}, failure("22008", "date out of range")
	}
	return Value{typ: d, integer: days}, nil
}

func (v Value) Date() (int64, error) {
	if v.null || v.typ == nil || v.typ.id != Date {
		return 0, fmt.Errorf("not a date value")
	}
	return v.integer, nil
}

func (d *Descriptor) dateFromCivil(text string, y, m, day int) (Value, error) {
	// Bound the year before civil arithmetic, without imposing TIMESTAMP's
	// much smaller range or multiplying days into overflowing microseconds.
	if y < -4713 || y > 5874897 {
		return Value{}, civilInputError("date", text, "range", false)
	}
	days := civilDay(y, m, day)
	if days < dateMinimum || days > dateMaximum {
		return Value{}, civilInputError("date", text, "range", false)
	}
	return DateValue(d, days)
}

// DateFromTime preserves the written date in t's own location, ignores clock
// fields, and uses Go's astronomical year zero as PostgreSQL 1 BC.
func DateFromTime(d *Descriptor, t time.Time) (Value, error) {
	if d == nil || d.id != Date {
		return Value{}, fmt.Errorf("invalid date descriptor")
	}
	y, m, day := t.Date()
	// Numeric year decoding precedes final DATE-domain validation.
	if y > math.MaxInt32 || y < 1-math.MaxInt32 {
		return Value{}, civilInputError("date", timestampTimeInput(t), "field", false)
	}
	if y < -4713 || y > 5874897 {
		return Value{}, civilInputError("date", timestampTimeInput(t), "range", false)
	}
	v, err := DateValue(d, civilDay(y, int(m), day))
	if err != nil {
		return Value{}, civilInputError("date", timestampTimeInput(t), "range", false)
	}
	return v, nil
}

func dateOutput(days int64) string {
	if days == DateNegativeInfinity {
		return "-infinity"
	}
	if days == DatePositiveInfinity {
		return "infinity"
	}
	y, m, day := dayCivil(days)
	bc := y <= 0
	if bc {
		y = 1 - y
	}
	text := fmt.Sprintf("%04d-%02d-%02d", y, m, day)
	if bc {
		text += " BC"
	}
	return text
}
