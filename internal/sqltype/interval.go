package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

// Field bit numbers and packed modifiers follow PostgreSQL's datetime.h.
const (
	IntervalMonth         int32 = 1 << 1
	IntervalYear          int32 = 1 << 2
	IntervalDay           int32 = 1 << 3
	IntervalHour          int32 = 1 << 10
	IntervalMinute        int32 = 1 << 11
	IntervalSecond        int32 = 1 << 12
	IntervalFullRange     int32 = 0x7fff
	IntervalFullPrecision int32 = 0xffff
)

type IntervalStyle uint8

const (
	IntervalPostgres IntervalStyle = iota
	IntervalPostgresVerbose
	IntervalSQLStandard
	IntervalISO8601
)

func ParseIntervalStyle(s string) IntervalStyle {
	switch s {
	case "postgres_verbose":
		return IntervalPostgresVerbose
	case "sql_standard":
		return IntervalSQLStandard
	case "iso_8601":
		return IntervalISO8601
	}
	return IntervalPostgres
}

var intervalRanges = [...]struct {
	mask int32
	name string
}{
	{IntervalYear, "YEAR"}, {IntervalMonth, "MONTH"}, {IntervalDay, "DAY"}, {IntervalHour, "HOUR"}, {IntervalMinute, "MINUTE"}, {IntervalSecond, "SECOND"},
	{IntervalYear | IntervalMonth, "YEAR TO MONTH"}, {IntervalDay | IntervalHour, "DAY TO HOUR"}, {IntervalDay | IntervalHour | IntervalMinute, "DAY TO MINUTE"}, {IntervalDay | IntervalHour | IntervalMinute | IntervalSecond, "DAY TO SECOND"},
	{IntervalHour | IntervalMinute, "HOUR TO MINUTE"}, {IntervalHour | IntervalMinute | IntervalSecond, "HOUR TO SECOND"}, {IntervalMinute | IntervalSecond, "MINUTE TO SECOND"}, {IntervalFullRange, ""},
}

func intervalRangeName(r int32) (string, bool) {
	for _, v := range intervalRanges {
		if r == v.mask {
			return v.name, true
		}
	}
	return "", false
}
func intervalTypmodValid(p int32) bool {
	if p == -1 {
		return true
	}
	_, ok := intervalRangeName(p >> 16)
	precision := p & 0xffff
	return p >= 0 && ok && (precision <= 6 || precision == IntervalFullPrecision)
}
func (d *Descriptor) intervalModifiers(mods []int32) (Usage, *sqlstate.Error, error) {
	u := d.Usage()
	if len(mods) < 1 || len(mods) > 2 {
		return u, nil, failure("22023", "invalid INTERVAL type modifier")
	}
	r := mods[0]
	if _, ok := intervalRangeName(r); !ok {
		return u, nil, failure("22023", "invalid INTERVAL type modifier")
	}
	p := IntervalFullPrecision
	if len(mods) == 2 {
		p = mods[1]
		if p < 0 {
			return u, nil, failure("22023", fmt.Sprintf("INTERVAL(%d) precision must not be negative", p))
		}
	}
	var warning *sqlstate.Error
	if len(mods) == 2 && p > 6 {
		warning = &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: fmt.Sprintf("INTERVAL(%d) precision reduced to maximum allowed, 6", p)}
		p = 6
	}
	if r != IntervalFullRange || p != IntervalFullPrecision {
		u.Typmod = r<<16 | p
	}
	return u, warning, nil
}
func intervalDeclaration(p int32) string {
	if p == -1 {
		return "INTERVAL"
	}
	if p&0xffff != IntervalFullPrecision && (p>>16)&IntervalSecond == 0 {
		return fmt.Sprintf(`"interval"(%d,%d)`, p>>16, p&0xffff)
	}
	name, _ := intervalRangeName(p >> 16)
	s := "INTERVAL"
	if name != "" {
		s += " " + name
	}
	if p&0xffff != IntervalFullPrecision {
		s += fmt.Sprintf("(%d)", p&0xffff)
	}
	return s
}
func IntervalValue(d *Descriptor, months, days int32, microseconds int64) (Value, error) {
	if d == nil || d.id != Interval {
		return Value{}, fmt.Errorf("invalid interval descriptor")
	}
	return Value{typ: d, months: months, days: days, integer: microseconds}, nil
}
func (v Value) Interval() (int32, int32, int64, error) {
	if v.null || v.typ == nil || v.typ.id != Interval {
		return 0, 0, 0, fmt.Errorf("not an interval value")
	}
	return v.months, v.days, v.integer, nil
}
func intervalInfinity(v Value) int {
	if v.months == math.MinInt32 && v.days == math.MinInt32 && v.integer == math.MinInt64 {
		return -1
	}
	if v.months == math.MaxInt32 && v.days == math.MaxInt32 && v.integer == math.MaxInt64 {
		return 1
	}
	return 0
}
func adjustInterval(v Value, p int32) (Value, error) {
	if p == -1 || intervalInfinity(v) != 0 {
		return v, nil
	}
	r := p >> 16
	switch r {
	case IntervalYear:
		v.months = v.months / 12 * 12
		v.days = 0
		v.integer = 0
	case IntervalMonth, IntervalYear | IntervalMonth:
		v.days = 0
		v.integer = 0
	case IntervalDay:
		v.integer = 0
	default:
		if r&IntervalSecond == 0 {
			unit := int64(60000000)
			if r&IntervalMinute == 0 {
				unit = 3600000000
			}
			v.integer = v.integer / unit * unit
		}
	}
	precision := p & 0xffff
	if precision < 6 {
		unit := int64(1)
		for i := precision; i < 6; i++ {
			unit *= 10
		}
		q, rem := v.integer/unit, v.integer%unit
		if rem >= unit/2 {
			q++
		} else if rem <= -unit/2 {
			q--
		}
		if q > math.MaxInt64/unit || q < math.MinInt64/unit {
			return Value{}, failure("22008", "interval out of range")
		}
		v.integer = q * unit
	}
	return v, nil
}

// Signed 128-bit spans make comparison/grouping allocation-free across the
// complete finite component domain, including cancellation between components.
func intervalSpan(v Value) (int64, uint64) {
	days := int64(v.months)*30 + int64(v.days)
	negative := days < 0
	magnitude := uint64(days)
	if negative {
		magnitude = uint64(-days)
	}
	hi, lo := bits.Mul64(magnitude, uint64(microsPerDay))
	if negative {
		lo, c := bits.Add64(^lo, 1, 0)
		hi = ^hi + c
		var carry uint64
		lo, carry = bits.Add64(lo, uint64(v.integer), 0)
		hi, _ = bits.Add64(hi, uint64(v.integer>>63), carry)
		return int64(hi), lo
	}
	var carry uint64
	lo, carry = bits.Add64(lo, uint64(v.integer), 0)
	hi, _ = bits.Add64(hi, uint64(v.integer>>63), carry)
	return int64(hi), lo
}
func compareIntervals(a, b Value) int {
	ai, bi := intervalInfinity(a), intervalInfinity(b)
	if ai != bi {
		if ai < bi {
			return -1
		}
		return 1
	}
	if ai != 0 {
		return 0
	}
	ah, al := intervalSpan(a)
	bh, bl := intervalSpan(b)
	if ah < bh || ah == bh && al < bl {
		return -1
	}
	if ah > bh || ah == bh && al > bl {
		return 1
	}
	return 0
}
func intervalMagnitude(n int64) uint64 {
	if n < 0 {
		return uint64(-(n + 1)) + 1
	}
	return uint64(n)
}
func intervalSeconds(n uint64, padded bool) string {
	s := strconv.FormatUint(n/1000000, 10)
	if padded && len(s) < 2 {
		s = "0" + s
	}
	if n%1000000 != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%06d", n%1000000), "0")
	}
	return s
}
func intervalClock(n int64, padded bool) string {
	u := intervalMagnitude(n)
	hours := strconv.FormatUint(u/3600000000, 10)
	if padded && len(hours) < 2 {
		hours = "0" + hours
	}
	return hours + fmt.Sprintf(":%02d:", u/60000000%60) + intervalSeconds(u%60000000, true)
}
func intervalOutput(v Value, style IntervalStyle) (string, error) {
	if style > IntervalISO8601 {
		return "", fmt.Errorf("invalid interval style")
	}
	if i := intervalInfinity(v); i != 0 {
		if i < 0 {
			return "-infinity", nil
		}
		return "infinity", nil
	}
	y, m, d, t := int64(v.months/12), int64(v.months%12), int64(v.days), v.integer
	if style == IntervalSQLStandard {
		negative := y < 0 || m < 0 || d < 0 || t < 0
		positive := y > 0 || m > 0 || d > 0 || t > 0
		ym := y != 0 || m != 0
		dt := d != 0 || t != 0
		if !negative && !positive {
			return "0", nil
		}
		if negative && positive || ym && dt {
			sign := func(n int64) string {
				if n < 0 {
					return "-"
				}
				return "+"
			}
			return fmt.Sprintf("%s%d-%d %s%d %s%s", sign(int64(v.months)), intervalMagnitude(y), intervalMagnitude(m), sign(d), intervalMagnitude(d), sign(t), intervalClock(t, false)), nil
		}
		prefix := ""
		if negative {
			prefix = "-"
		}
		if ym {
			return fmt.Sprintf("%s%d-%d", prefix, intervalMagnitude(y), intervalMagnitude(m)), nil
		}
		if d != 0 {
			return fmt.Sprintf("%s%d %s", prefix, intervalMagnitude(d), intervalClock(t, false)), nil
		}
		return prefix + intervalClock(t, false), nil
	}
	if style == IntervalISO8601 {
		if y == 0 && m == 0 && d == 0 && t == 0 {
			return "PT0S", nil
		}
		s := "P"
		for _, part := range []struct {
			n    int64
			unit string
		}{{y, "Y"}, {m, "M"}, {d, "D"}} {
			if part.n != 0 {
				s += strconv.FormatInt(part.n, 10) + part.unit
			}
		}
		if t != 0 {
			s += "T"
			h, mi := t/3600000000, t/60000000%60
			if h != 0 {
				s += strconv.FormatInt(h, 10) + "H"
			}
			if mi != 0 {
				s += strconv.FormatInt(mi, 10) + "M"
			}
			if t%60000000 != 0 {
				if t < 0 {
					s += "-"
				}
				s += intervalSeconds(intervalMagnitude(t%60000000), false) + "S"
			}
		}
		return s, nil
	}
	var partArray [7]string
	parts := partArray[:0]
	before := false
	add := func(n int64, unit string, verbose bool) {
		if n == 0 {
			return
		}
		text := strconv.FormatInt(n, 10)
		if verbose {
			if len(parts) == 0 {
				before = n < 0
			}
			if before {
				n = -n
				text = strconv.FormatInt(n, 10)
			}
			if n != 1 {
				unit += "s"
			}
		} else {
			if n > 0 && before {
				text = "+" + text
			}
			if n != 1 {
				unit += "s"
			}
			before = n < 0
		}
		parts = append(parts, text+" "+unit)
	}
	verbose := style == IntervalPostgresVerbose
	add(y, "year", verbose)
	add(m, "mon", verbose)
	add(d, "day", verbose)
	if verbose {
		add(t/3600000000, "hour", true)
		add(t/60000000%60, "min", true)
		if t%60000000 != 0 {
			n := t % 60000000
			if len(parts) == 0 {
				before = n < 0
			}
			prefix := ""
			if n < 0 && !before || n > 0 && before {
				prefix = "-"
			}
			unit := "secs"
			if intervalMagnitude(n) == 1000000 {
				unit = "sec"
			}
			parts = append(parts, prefix+intervalSeconds(intervalMagnitude(n), false)+" "+unit)
		}
		if len(parts) == 0 {
			return "@ 0", nil
		}
		s := "@ " + strings.Join(parts, " ")
		if before {
			s += " ago"
		}
		return s, nil
	}
	if t != 0 || len(parts) == 0 {
		prefix := ""
		if t < 0 {
			prefix = "-"
		} else if before {
			prefix = "+"
		}
		parts = append(parts, prefix+intervalClock(t, true))
	}
	return strings.Join(parts, " "), nil
}
func intervalCanonical(v Value) string {
	if intervalInfinity(v) != 0 {
		s, _ := intervalOutput(v, IntervalPostgres)
		return s
	}
	sign := func(n int64) string {
		if n < 0 {
			return "-"
		}
		return "+"
	}
	return fmt.Sprintf("%+d months %+d days %s%s", v.months, v.days, sign(v.integer), intervalClock(v.integer, true))
}

// Accumulate years separately: decoder overflow (22015) and final year-to-month
// overflow (22008) are distinct PostgreSQL diagnostics.
type intervalParts struct {
	years, months, days int64
	time                int64
}

func intervalAdd(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}
func intervalMultiply(a, b int64) (int64, bool) {
	if a > math.MaxInt64/b || a < math.MinInt64/b {
		return 0, false
	}
	return a * b, true
}
func (p *intervalParts) add(unit string, n int64, f float64) bool {
	add32 := func(dst *int64, n int64) bool {
		sum, ok := intervalAdd(*dst, n)
		if !ok || sum < math.MinInt32 || sum > math.MaxInt32 {
			return false
		}
		*dst = sum
		return true
	}
	addTime := func(n int64) bool {
		sum, ok := intervalAdd(p.time, n)
		if ok {
			p.time = sum
		}
		return ok
	}
	fractTime := func(f float64, scale int64) bool {
		x := f * float64(scale)
		if x >= 9223372036854775808.0 || x < -9223372036854775808.0 {
			return false
		}
		n := int64(x)
		rem := x - float64(n)
		if rem > .5 {
			n++
		} else if rem < -.5 {
			n--
		}
		return addTime(n)
	}
	fractDays := func(f float64, scale int64) bool {
		x := f * float64(scale)
		n := int64(x)
		return add32(&p.days, n) && fractTime(x-float64(n), microsPerDay)
	}
	switch unit {
	case "year", "decade", "century", "millennium":
		scale := int64(1)
		switch unit {
		case "decade":
			scale = 10
		case "century":
			scale = 100
		case "millennium":
			scale = 1000
		}
		years, ok := intervalMultiply(n, scale)
		return ok && add32(&p.years, years) && add32(&p.months, int64(math.RoundToEven(f*float64(scale)*12)))
	case "month":
		return add32(&p.months, n) && fractDays(f, 30)
	case "week":
		days, ok := intervalMultiply(n, 7)
		return ok && add32(&p.days, days) && fractDays(f, 7)
	case "day":
		return add32(&p.days, n) && fractTime(f, microsPerDay)
	default:
		scale := int64(1)
		switch unit {
		case "hour":
			scale = 3600000000
		case "minute":
			scale = 60000000
		case "second":
			scale = 1000000
		case "millisecond":
			scale = 1000
		case "microsecond":
		default:
			return false
		}
		whole, ok := intervalMultiply(n, scale)
		return ok && addTime(whole) && fractTime(f, scale)
	}
}
func intervalUnit(s string) (string, uint32) {
	if len(s) > 10 {
		s = s[:10]
	}
	switch strings.ToLower(s) {
	case "y", "year", "years", "yr", "yrs":
		return "year", 1
	case "mon", "mons", "month", "months":
		return "month", 2
	case "d", "day", "days":
		return "day", 4
	case "h", "hour", "hours", "hr", "hrs":
		return "hour", 8
	case "m", "min", "mins", "minute", "minutes":
		return "minute", 16
	case "s", "sec", "secs", "second", "seconds":
		return "second", 32
	case "ms", "msec", "msecs", "mseconds", "millisecon":
		return "millisecond", 64
	case "us", "usec", "usecs", "useconds", "microsecon":
		return "microsecond", 128
	case "w", "week", "weeks":
		return "week", 256
	case "decade", "decades", "dec", "decs":
		return "decade", 512
	case "century", "centuries", "cent", "c":
		return "century", 1024
	case "millennium", "millennia", "mil", "mils":
		return "millennium", 2048
	}
	return "", 0
}
func intervalNumber(s string) (int64, float64, bool, bool) {
	// Keep the integer exact, especially at int64 endpoints.
	dot := strings.IndexByte(s, '.')
	whole := s
	fraction := ""
	if dot >= 0 {
		whole = s[:dot]
		fraction = s[dot:]
	}
	if whole == "" || whole == "+" || whole == "-" {
		whole += "0"
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, 0, false, errors.Is(err, strconv.ErrRange)
	}
	f := float64(0)
	if fraction != "" {
		var e error
		f, e = strconv.ParseFloat("0"+fraction, 64)
		if e != nil {
			return 0, 0, false, false
		}
		if s[0] == '-' {
			f = -f
		}
	}
	return n, f, true, false
}
func intervalDefaultUnit(r int32) string {
	if r == IntervalYear {
		return "year"
	}
	if r == IntervalMonth || r == IntervalYear|IntervalMonth {
		return "month"
	}
	if r == IntervalDay {
		return "day"
	}
	if r&IntervalSecond != 0 {
		return "second"
	}
	if r&IntervalMinute != 0 {
		return "minute"
	}
	if r&IntervalHour != 0 {
		return "hour"
	}
	return "second"
}
func (d *Descriptor) intervalInput(ctx context.Context, original string, pmod int32, env InputEnvironment) (Value, error) {
	bad := func() (Value, error) {
		return Value{}, failure("22007", fmt.Sprintf("invalid input syntax for type interval: \"%s\"", original))
	}
	overflow := func() (Value, error) {
		return Value{}, failure("22015", fmt.Sprintf("interval field value out of range: \"%s\"", original))
	}
	s := strings.TrimSpace(original)
	if len(s) == 0 {
		return bad()
	}
	switch strings.ToLower(s) {
	case "infinity", "+infinity":
		return IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MaxInt64)
	case "-infinity":
		return IntervalValue(d, math.MinInt32, math.MinInt32, math.MinInt64)
	}
	var parts intervalParts
	if s[0] == 'P' {
		ok, rangeError := parts.iso(ctx, s)
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if !ok {
			if rangeError {
				return overflow()
			}
			return bad()
		}
	} else {
		fields, nf, ok := tokenizeDateTime(s, 256)
		if !ok || nf == 0 {
			return bad()
		}
		var tokenArray [25]string
		for i := range nf {
			token := fields[i].text
			if len(token) > 1 && (token[0] == '+' || token[0] == '-') && space(token[1]) {
				token = token[:1] + strings.TrimLeft(token[1:], " \t\n\r\f\v")
			}
			tokenArray[i] = token
		}
		tokens := tokenArray[:nf]
		ago := false
		if strings.EqualFold(tokens[len(tokens)-1], "ago") {
			ago = true
			tokens = tokens[:len(tokens)-1]
		}
		if len(tokens) == 0 {
			return bad()
		}
		force := env.IntervalStyle == IntervalSQLStandard && strings.HasPrefix(tokens[0], "-")
		for _, token := range tokens[1:] {
			if strings.HasPrefix(token, "+") || strings.HasPrefix(token, "-") {
				force = false
			}
		}
		r := IntervalFullRange
		if pmod >= 0 {
			r = pmod >> 16
		}
		unit := intervalDefaultUnit(r)
		var mask uint32
		for i := len(tokens) - 1; i >= 0; i-- {
			tok := tokens[i]
			if u, _ := intervalUnit(tok); u != "" {
				unit = u
				i--
				if i < 0 {
					return bad()
				}
				tok = tokens[i]
			}
			chosen := unit
			var field uint32
			if strings.Contains(tok, ":") {
				value, ok, rangeError := intervalParseClock(tok, r)
				if !ok {
					if rangeError && tok[0] != '+' && tok[0] != '-' {
						return overflow()
					}
					return bad()
				}
				if force && value > 0 {
					value = -value
				}
				parts.time = value
				field = 8 | 16 | 32 | 64 | 128
				unit = "day"
			} else {
				// SQL year-month fields have a second dash after the leading sign.
				start := 0
				if tok[0] == '+' || tok[0] == '-' {
					start = 1
				}
				dash := strings.IndexByte(tok[start:], '-')
				var n int64
				var f float64
				var ok, rangeError bool
				if dash >= 0 {
					dash += start
					years, e := strconv.ParseInt(tok[:dash], 10, 64)
					monthText := tok[dash+1:]
					months := int64(0)
					var e2 error
					if monthText != "" {
						months, e2 = strconv.ParseInt(monthText, 10, 64)
					}
					if errors.Is(e, strconv.ErrRange) || errors.Is(e2, strconv.ErrRange) || months < 0 || months >= 12 {
						return overflow()
					}
					if e != nil || e2 != nil {
						return bad()
					}
					n, ok = intervalMultiply(years, 12)
					if !ok {
						return overflow()
					}
					if tok[0] == '-' {
						months = -months
					}
					n, ok = intervalAdd(n, months)
					if !ok {
						return overflow()
					}
					chosen = "month"
					unit = "month"
				} else {
					n, f, ok, rangeError = intervalNumber(tok)
					if !ok {
						if rangeError {
							return overflow()
						}
						return bad()
					}
				}
				if force {
					if n > 0 {
						n = -n
					}
					if f > 0 {
						f = -f
					}
				}
				if !parts.add(chosen, n, f) {
					return overflow()
				}
				_, field = intervalUnit(chosen)
				if chosen == "second" && f != 0 {
					field = 32 | 64 | 128
				}
				if chosen == "hour" {
					unit = "day"
				}
			}
			if mask&field != 0 {
				return bad()
			}
			mask |= field
		}
		if mask == 0 {
			return bad()
		}
		if ago {
			if parts.years == math.MinInt32 || parts.months == math.MinInt32 || parts.days == math.MinInt32 || parts.time == math.MinInt64 {
				return overflow()
			}
			parts.years = -parts.years
			parts.months = -parts.months
			parts.days = -parts.days
			parts.time = -parts.time
		}
	}
	months := parts.years*12 + parts.months
	if months < math.MinInt32 || months > math.MaxInt32 {
		return Value{}, failure("22008", "interval out of range")
	}
	v, _ := IntervalValue(d, int32(months), int32(parts.days), parts.time)
	return adjustInterval(v, pmod)
}
func intervalParseClock(s string, r int32) (int64, bool, bool) {
	negative := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		negative = s[0] == '-'
		s = s[1:]
	}
	fields := strings.Split(s, ":")
	if len(fields) < 2 || len(fields) > 3 {
		return 0, false, false
	}
	hour, e := strconv.ParseInt(fields[0], 10, 64)
	if e != nil {
		return 0, false, errors.Is(e, strconv.ErrRange)
	}
	if hour < 0 {
		return 0, false, true
	}
	minute, e := strconv.ParseInt(fields[1], 10, 64)
	var seconds int64
	var f float64
	if len(fields) == 2 && (r == IntervalMinute|IntervalSecond || strings.Contains(fields[1], ".")) {
		minute = hour
		hour = 0
		var ok, over bool
		seconds, f, ok, over = intervalNumber(fields[1])
		if !ok {
			return 0, false, over
		}
	} else {
		if e != nil {
			return 0, false, false
		}
		if len(fields) == 3 {
			var ok, over bool
			seconds, f, ok, over = intervalNumber(fields[2])
			if !ok {
				return 0, false, over
			}
		}
	}
	if minute < 0 || minute > 59 || seconds < 0 || seconds > 60 || f < 0 {
		return 0, false, true
	}
	hours, ok := intervalMultiply(hour, 3600000000)
	if !ok {
		return 0, false, true
	}
	minutes, ok := intervalMultiply(minute, 60000000)
	if !ok {
		return 0, false, true
	}
	tail, ok := intervalAdd(minutes, seconds*1000000+int64(math.RoundToEven(f*1000000)))
	if !ok {
		return 0, false, true
	}
	if negative {
		hours = -hours
		tail = -tail
	}
	result, ok := intervalAdd(hours, tail)
	return result, ok, !ok
}
func isoNumber(s string) (int64, float64, int, bool, bool) {
	if s == "" || s[0] == '+' {
		return 0, 0, 0, false, false
	}
	i := 0
	if s[0] == '-' {
		i++
		// The release's leading-minus guard admits strtod's nonfinite
		// prefixes, which fail magnitude validation before unit decoding.
		if len(s) >= 4 && (strings.EqualFold(s[1:4], "nan") || strings.EqualFold(s[1:4], "inf")) {
			return 0, 0, 0, false, true
		}
	}
	start := i
	hex := i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X')
	if hex {
		i += 2
	}
	digits, nonzero := false, false
	for i < len(s) && isoNumberDigit(s[i], hex) {
		digits = true
		nonzero = nonzero || s[i] != '0'
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isoNumberDigit(s[i], hex) {
			digits = true
			nonzero = nonzero || s[i] != '0'
			i++
		}
	}
	if !digits {
		if !hex {
			return 0, 0, 0, false, false
		}
		// strtod falls back to the initial zero for an incomplete 0x prefix.
		i, hex = start+1, false
	}
	exponent := false
	if i < len(s) && (hex && (s[i] == 'p' || s[i] == 'P') || !hex && (s[i] == 'e' || s[i] == 'E')) {
		marker := i
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		digitsStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == digitsStart {
			i = marker
		} else {
			exponent = true
		}
	}
	number := s[:i]
	if hex && !exponent {
		// ParseFloat requires p even though strtod's hexadecimal exponent
		// is optional. Only the conversion token changes, not consumed width.
		number += "p0"
	}
	n, e := strconv.ParseFloat(number, 64)
	if e != nil || isoNumberUnderflow(s[:i], n, nonzero) {
		return 0, 0, 0, false, false
	}
	if math.Abs(n) > 1e15 {
		return 0, 0, 0, false, true
	}
	whole := int64(n)
	return whole, n - float64(whole), i, true, false
}

func isoNumberDigit(c byte, hex bool) bool {
	return c >= '0' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
}

// PostgreSQL's ParseISO8601Number rejects strtod errno, unlike ParseFloat.
// Match glibc-2.36 round_and_return behavior exercised by the pinned oracle,
// including its discarded pre-denormal half bit (strtod_l.c:214-320):
// https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/stdlib/strtod_l.c
// Only zero/subnormal/minimum-normal results need this exceptional path.
func isoNumberUnderflow(raw string, parsed float64, nonzero bool) bool {
	if math.Abs(parsed) > 0x1p-1022 {
		return false
	}
	if parsed == 0 {
		return nonzero
	}
	// Keep the normal 53 mantissa bits, the original half bit, and an
	// independent sticky flag. Arbitrary precision is necessary only here.
	// Parse exactly before the single binary rounding: Float.Parse scales
	// decimal exponents with a rounded power of five, losing exactness.
	var exact big.Rat
	if _, ok := exact.SetString(raw); !ok {
		return true
	}
	var value big.Float
	value.SetPrec(54).SetMode(big.ToZero)
	value.SetRat(&exact)
	sticky := value.Acc() != big.Exact
	value.Abs(&value)
	var mantissa big.Float
	exponent := value.MantExp(&mantissa) - 1
	if exponent >= -1022 {
		return false
	}
	if exponent < -1075 {
		return true
	}
	mantissa.SetMantExp(&mantissa, 54)
	bits54, _ := mantissa.Uint64()
	bits53 := bits54 >> 1
	shift := uint(-1022 - exponent)
	// Tininess is tested after normal-precision nearest-even rounding.
	if shift == 1 && bits53 == (1<<53)-1 && bits54&1 != 0 {
		return false
	}
	// glibc-2.36 replaces, rather than retains, the original half bit
	// during denormalization. Exact subnormals therefore remain valid.
	return sticky || bits53&((uint64(1)<<shift)-1) != 0
}

func isoIntegerWidth(s string) int {
	start := 0
	if len(s) > 0 && s[0] == '-' {
		start = 1
	}
	end := start
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return end - start
}
func (p *intervalParts) iso(ctx context.Context, s string) (bool, bool) {
	s = s[1:]
	if s == "" {
		return false, false
	}
	date := true
	have := false
	for len(s) > 0 {
		if ctx.Err() != nil {
			return false, false
		}
		if s[0] == 'T' {
			date = false
			have = false
			s = s[1:]
			continue
		}
		n, f, width, ok, rangeError := isoNumber(s)
		if !ok {
			return false, rangeError
		}
		raw := s[:width]
		s = s[width:]
		u := byte(0)
		if len(s) > 0 {
			u = s[0]
			s = s[1:]
		}
		unit := ""
		if date {
			switch u {
			case 'Y':
				unit = "year"
			case 'M':
				unit = "month"
			case 'W':
				unit = "week"
			case 'D':
				unit = "day"
			}
		} else {
			switch u {
			case 'H':
				unit = "hour"
			case 'M':
				unit = "minute"
			case 'S':
				unit = "second"
			}
		}
		if unit != "" {
			if !p.add(unit, n, f) {
				return false, true
			}
			have = true
			continue
		}
		if have {
			return false, false
		}
		if date && (u == 0 || u == 'T' || u == '-') {
			integerWidth := isoIntegerWidth(raw)
			if integerWidth == 8 && (u == 0 || u == 'T') {
				if !p.add("year", n/10000, 0) || !p.add("month", n/100%100, 0) || !p.add("day", n%100, f) {
					return false, true
				}
				if u == 0 {
					return true, false
				}
				date = false
				have = false
				continue
			}
			if !p.add("year", n, f) {
				return false, true
			}
			if u == 0 {
				return true, false
			}
			if u == 'T' {
				date = false
				continue
			}
			n, f, width, ok, rangeError = isoNumber(s)
			if !ok {
				return false, rangeError
			}
			s = s[width:]
			if !p.add("month", n, f) {
				return false, true
			}
			if s == "" {
				return true, false
			}
			if s[0] == 'T' {
				date = false
				s = s[1:]
				continue
			}
			if s[0] != '-' {
				return false, false
			}
			s = s[1:]
			n, f, width, ok, rangeError = isoNumber(s)
			if !ok {
				return false, rangeError
			}
			s = s[width:]
			if !p.add("day", n, f) {
				return false, true
			}
			if s == "" {
				return true, false
			}
			if s[0] == 'T' {
				date = false
				s = s[1:]
				continue
			}
			return false, false
		}
		if !date && (u == 0 || u == ':') {
			if isoIntegerWidth(raw) == 6 && u == 0 {
				if !p.add("hour", n/10000, 0) || !p.add("minute", n/100%100, 0) || !p.add("second", n%100, 0) || !p.add("microsecond", 0, f) {
					return false, true
				}
				return true, false
			}
			if !p.add("hour", n, f) {
				return false, true
			}
			if u == 0 {
				return true, false
			}
			n, f, width, ok, rangeError = isoNumber(s)
			if !ok {
				return false, rangeError
			}
			s = s[width:]
			if !p.add("minute", n, f) {
				return false, true
			}
			if s == "" {
				return true, false
			}
			if s[0] != ':' {
				return false, false
			}
			s = s[1:]
			n, f, width, ok, rangeError = isoNumber(s)
			if !ok {
				return false, rangeError
			}
			s = s[width:]
			if !p.add("second", n, f) {
				return false, true
			}
			return s == "", false
		}
		return false, false
	}
	return true, false
}
