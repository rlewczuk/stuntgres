package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"unicode/utf8"
)

type datetimeFields struct {
	year, yearsize, month, day, weekday, doy, week, monthweek, julian, century int
	hour, minute, second, seconds, ms, us, precision, pm, bc                   int
	zoneHour, zoneMinute, zoneSign                                             int
	clock12                                                                    bool
	mode                                                                       uint8
	zone                                                                       zoneSpecification
}

func datetimeInput(ctx context.Context, result *Descriptor, source string, nodes []datetimeNode, env InputEnvironment, conflict bool) (Value, error) {
	fields := datetimeFields{zone: zoneSpecification{zone: env.Zone}}
	position, extra := 0, 0
	fixed := false
	for i, node := range nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		if position >= len(source) {
			break
		}
		if !fixed && (node.key == nil || node.key.kind != "FX") && (node.key != nil || i == 0) {
			for position < len(source) && datetimeSpace(source[position]) {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
				extra++
			}
		}
		if node.key == nil {
			// This admitted literal can exhaust whitespace without demanding a
			// field reader. Numeric/name actions still run with an empty fragment.
			if position >= len(source) {
				continue
			}
			if node.class != 0 {
				if fixed {
					_, width := utf8.DecodeRuneInString(source[position:])
					position += width
				} else {
					extra--
					if datetimeSpace(source[position]) || datetimeSeparator(source[position]) {
						if position%1024 == 0 {
							if err := ctx.Err(); err != nil {
								return Value{}, err
							}
						}
						position++
						extra++
					}
				}
			} else if fixed || extra <= 0 {
				_, width := utf8.DecodeRuneInString(source[position:])
				position += width
			} else {
				extra--
			}
			continue
		}
		if node.key.mode != 0 {
			if fields.mode != 0 && fields.mode != node.key.mode {
				return Value{}, datetimeError("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")
			}
			fields.mode = node.key.mode
		}
		key := node.key.kind
		var destination *int
		width := len(node.key.word)
		numeric := true
		scale := 0
		switch key {
		case "FX":
			fixed = true
			numeric = false
		case "HH", "HH12":
			fields.clock12 = true
			destination = &fields.hour
			width = 2
		case "HH24":
			destination = &fields.hour
			width = 2
		case "MI":
			destination = &fields.minute
		case "SS":
			destination = &fields.second
		case "SSSS":
			destination = &fields.seconds
		case "MS":
			destination = &fields.ms
			width = 3
			scale = 3
		case "US":
			destination = &fields.us
			width = 6
			scale = 6
		case "FF1", "FF2", "FF3", "FF4", "FF5", "FF6":
			width = int(key[2] - '0')
			fields.precision = width
			destination = &fields.us
			scale = 6
		case "YYYY", "IYYY", "YYY", "IYY", "YY", "IY", "Y", "I":
			destination = &fields.year
		case "MM":
			destination = &fields.month
		case "DD":
			destination = &fields.day
		case "DDD", "IDDD":
			destination = &fields.doy
			width = 3
		case "D", "ID":
			destination = &fields.weekday
			width = 1
		case "WW", "IW":
			destination = &fields.week
		case "W":
			destination = &fields.monthweek
		case "J":
			destination = &fields.julian
		case "CC":
			destination = &fields.century
		case "Q":
			destination = nil
		case "MONTH", "Month", "month", "MON", "Mon", "mon", "DAY", "Day", "day", "DY", "Dy", "dy", "RM", "rm", "AM", "PM", "am", "pm", "A_M", "P_M", "a_m", "p_m", "AD", "BC", "ad", "bc", "A_D", "B_C", "a_d", "b_c":
			numeric = false
			var names []string
			short, reverse, localized := false, false, false
			adjust := 0
			switch key {
			case "MONTH", "Month", "month":
				localized = node.tm
				names = datetimeMonths[:]
				if node.tm {
					names = env.TimeLocale.policy().months[:]
				}
				destination = &fields.month
				adjust = 1
			case "MON", "Mon", "mon":
				localized = node.tm
				names = datetimeMonths[:]
				if node.tm {
					names = env.TimeLocale.policy().months[:]
				}
				destination = &fields.month
				adjust = 1
				short = true
			case "DAY", "Day", "day":
				localized = node.tm
				names = datetimeDays[:]
				if node.tm {
					names = env.TimeLocale.policy().days[:]
				}
				destination = &fields.weekday
			case "DY", "Dy", "dy":
				localized = node.tm
				names = datetimeDays[:]
				if node.tm {
					names = env.TimeLocale.policy().days[:]
				}
				destination = &fields.weekday
				short = true
			case "RM", "rm":
				names = datetimeRomans[:]
				destination = &fields.month
				adjust = 1
				reverse = true
			case "AM", "PM", "am", "pm":
				names = []string{"AM", "PM"}
				destination = &fields.pm
				fields.clock12 = true
			case "A_M", "P_M", "a_m", "p_m":
				names = []string{"A.M.", "P.M."}
				destination = &fields.pm
				fields.clock12 = true
			case "AD", "BC", "ad", "bc":
				names = []string{"AD", "BC"}
				destination = &fields.bc
			default:
				names = []string{"A.D.", "B.C."}
				destination = &fields.bc
			}
			value, err := datetimeMatchName(ctx, source, &position, node, names, short, reverse, localized, conflict)
			if err != nil {
				return Value{}, err
			}
			if err := datetimeSet(destination, value+adjust, node); err != nil {
				return Value{}, err
			}
			if key == "DAY" || key == "Day" || key == "day" || key == "DY" || key == "Dy" || key == "dy" {
				fields.weekday++
			}
		case "Y_YYY":
			numeric = false
			start := position
			for position < len(source) && datetimeSpace(source[position]) {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
			}
			first := position
			if position < len(source) && (source[position] == '+' || source[position] == '-') {
				position++
			}
			for position < len(source) && source[position] >= '0' && source[position] <= '9' {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
			}
			if position >= len(source) || source[position] != ',' {
				return Value{}, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", source[start:], "Y,YYY"), "", "")
			}
			thousands, err1 := strconv.ParseInt(source[first:position], 10, 64)
			position++
			for position < len(source) && datetimeSpace(source[position]) {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
			}
			last := position
			end := min(len(source), position+3)
			if position < end && (source[position] == '+' || source[position] == '-') {
				position++
			}
			for position < end && source[position] >= '0' && source[position] <= '9' {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
			}
			units, err2 := strconv.ParseInt(source[last:position], 10, 64)
			if err1 != nil || err2 != nil {
				return Value{}, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", source[start:], "Y,YYY"), "", "")
			}
			if thousands < -2147483 || thousands > 2147483 || !datetimeInt32(thousands*1000+units) {
				return Value{}, datetimeError("22008", `value for "Y,YYY" in source string is out of range`, "", "")
			}
			if err := datetimeSet(&fields.year, int(thousands*1000+units), node); err != nil {
				return Value{}, err
			}
			fields.yearsize = 4
			if node.ordinal != 0 {
				position = min(len(source), position+2)
			}
		case "TZ", "tz", "OF", "TZH", "TZM":
			numeric = false
			if key == "TZ" || key == "tz" {
				length := 0
				for length < min(len(source)-position, 255) && ((source[position+length] >= 'A' && source[position+length] <= 'Z') || (source[position+length] >= 'a' && source[position+length] <= 'z')) {
					length++
				}
				matched := false
				for size := length; size > 0; size-- {
					name := source[position : position+size]
					if inputAbbreviationKind(name, env) != timestampUnknownZone {
						zone, err := resolveInputZone(name, env)
						if err != nil {
							return Value{}, err
						}
						fields.zone = zone
						fields.zoneSign = 0
						position += size
						matched = true
						break
					}
				}
				if matched {
					break
				}
				if length > 0 {
					return Value{}, datetimeError("22007", fmt.Sprintf("invalid value %q for %q", source[position:], node.key.word), "Time zone abbreviation is not recognized.", "")
				}
			}
			if key != "TZM" {
				fields.zoneSign = 1
				if position < len(source) && (source[position] == '+' || source[position] == '-' || source[position] == ' ') {
					if source[position] == '-' {
						fields.zoneSign = -1
					}
					position++
				} else if extra > 0 && position > 0 && source[position-1] == '-' {
					fields.zoneSign = -1
				}
				value, _, err := datetimeInteger(ctx, source, &position, 2, node)
				if err != nil {
					return Value{}, err
				}
				if err := datetimeSet(&fields.zoneHour, value, node); err != nil {
					return Value{}, err
				}
				if key != "TZH" && position < len(source) && source[position] == ':' {
					position++
					value, _, err = datetimeInteger(ctx, source, &position, 2, node)
					if err != nil {
						return Value{}, err
					}
					if err := datetimeSet(&fields.zoneMinute, value, node); err != nil {
						return Value{}, err
					}
				}
			} else {
				if fields.zoneSign == 0 {
					fields.zoneSign = 1
				}
				value, _, err := datetimeInteger(ctx, source, &position, 2, node)
				if err != nil {
					return Value{}, err
				}
				if err := datetimeSet(&fields.zoneMinute, value, node); err != nil {
					return Value{}, err
				}
			}
		}
		if numeric {
			value, used, err := datetimeInteger(ctx, source, &position, width, node)
			if err != nil {
				return Value{}, err
			}
			if err := datetimeSet(destination, value, node); err != nil {
				return Value{}, err
			}
			if scale != 0 {
				factor := 1
				for power := used; power < scale; power++ {
					factor *= 10
				}
				if used > 0 && used < scale {
					*destination *= factor
				}
			}
			switch key {
			case "YYYY", "IYYY":
				fields.yearsize = 4
			case "YYY", "IYY", "YY", "IY", "Y", "I":
				if used < 4 {
					fields.year = datetimePartialYear(fields.year)
				}
				fields.yearsize = width
			case "ID":
				fields.weekday++
				if fields.weekday > 7 {
					fields.weekday = 1
				}
			}
			if node.ordinal != 0 {
				position = min(len(source), position+2)
			}
		}
		if !fixed {
			extra = 0
			for position < len(source) && datetimeSpace(source[position]) {
				if position%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return Value{}, err
					}
				}
				position++
				extra++
			}
		}
	}
	return fields.complete(result, source, env)
}

func (f datetimeFields) complete(result *Descriptor, source string, env InputEnvironment) (Value, error) {
	overflow := func() (Value, error) {
		return Value{}, datetimeError("22008", fmt.Sprintf("date/time field value out of range: %q", source), "", "")
	}
	h, minute, second := f.seconds/3600, f.seconds%3600/60, f.seconds%60
	if f.hour != 0 {
		h = f.hour
	}
	if f.minute != 0 {
		minute = f.minute
	}
	if f.second != 0 {
		second = f.second
	}
	if f.clock12 {
		if h < 1 || h > 12 {
			return Value{}, datetimeError("22007", fmt.Sprintf("hour %q is invalid for the 12-hour clock", strconv.Itoa(h)), "", "Use the 24-hour clock, or give an hour between 1 and 12.")
		}
		if f.pm != 0 && h < 12 {
			h += 12
		} else if f.pm == 0 && h == 12 {
			h = 0
		}
	}
	y, m, d := 0, 1, 1
	if f.year != 0 {
		if f.century != 0 && f.yearsize <= 2 {
			cc := int64(f.century)
			if f.bc != 0 {
				cc = -cc
			}
			yy := int64(f.year % 100)
			if yy != 0 {
				if cc >= 0 {
					yy += (cc - 1) * 100
				} else {
					yy = (cc+1)*100 - yy + 1
				}
			} else {
				yy = cc * 100
				if cc < 0 {
					yy++
				}
			}
			if !datetimeInt32(yy) {
				return overflow()
			}
			y = int(yy)
		} else {
			y = f.year
			if f.bc != 0 {
				y = -y
			}
			if y < 0 {
				y++
			}
		}
	} else if f.century != 0 {
		cc := int64(f.century)
		if f.bc != 0 {
			cc = -cc
		}
		yy := cc*100 + 1
		if cc >= 0 {
			yy = (cc-1)*100 + 1
		}
		if !datetimeInt32(yy) {
			return overflow()
		}
		y = int(yy)
	}
	if f.julian != 0 {
		y, m, d = dayCivil(int64(f.julian) - 2451545)
	}
	if f.week != 0 {
		if f.mode == 2 {
			offset := int64(f.week-1) * 7
			if f.weekday != 0 {
				if f.weekday == 1 {
					offset += 6
				} else {
					offset += int64(f.weekday - 2)
				}
			}
			y, m, d = dayCivil(datetimeISOStart(y) + offset)
		} else {
			value := int64(f.week-1)*7 + 1
			if !datetimeInt32(value) {
				return overflow()
			}
			f.doy = int(value)
		}
	}
	if f.monthweek != 0 {
		value := int64(f.monthweek-1)*7 + 1
		if !datetimeInt32(value) {
			return overflow()
		}
		f.day = int(value)
	}
	if f.day != 0 {
		d = f.day
	}
	if f.month != 0 {
		m = f.month
	}
	if f.doy != 0 && (m <= 1 || d <= 1) {
		if y == 0 && f.bc == 0 {
			return Value{}, datetimeError("22007", "cannot calculate day of year without year information", "", "")
		}
		if f.mode == 2 {
			y, m, d = dayCivil(datetimeISOStart(y) + int64(f.doy) - 1)
		} else {
			month, remaining := 1, f.doy
			for month <= 12 && remaining > monthDays(y, month) {
				remaining -= monthDays(y, month)
				month++
			}
			if m <= 1 {
				m = month
			}
			if d <= 1 {
				d = remaining
			}
		}
	}
	fraction := int64(f.ms)*1000 + int64(f.us)
	if !datetimeInt32(int64(f.ms)*1000) || !datetimeInt32(fraction) || m < 1 || m > 12 || d < 1 || d > monthDays(y, m) || h < 0 || h >= 24 || minute < 0 || minute >= 60 || second < 0 || second >= 60 || fraction < 0 || fraction >= 1000000 {
		return overflow()
	}
	if f.zoneSign != 0 {
		if f.zoneHour < 0 || f.zoneHour > 15 || f.zoneMinute < 0 || f.zoneMinute >= 60 {
			return Value{}, datetimeError("22009", fmt.Sprintf("time zone displacement out of range: %q", source), "", "")
		}
		f.zone = zoneSpecification{fixed: true, east: int32(f.zoneSign * (f.zoneHour*3600 + f.zoneMinute*60))}
	}
	if result.id == Date {
		return result.dateFromCivil(source, y, m, d)
	}
	n, err := civilTimestamp(y, m, d, h, minute, second, int(fraction))
	if err != nil {
		return Value{}, failure("22008", "timestamp out of range")
	}
	n -= int64(f.zone.offsetCivil(floorDiv(n, 1000000)+946684800)) * 1000000
	if n < timestampMinimum || n >= timestampEnd {
		return Value{}, failure("22008", "timestamp out of range")
	}
	precision := int32(-1)
	if f.precision > 0 {
		precision = int32(f.precision)
	}
	return TimestampValue(result, roundTimestamp(n, precision))
}
