package sqltype

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// ZoneInterval is private SET TIME ZONE syntax, not a general SQL type.
type ZoneInterval struct {
	Months, Days int64
	Microseconds int64
}

func DecodeZoneInterval(text, qualifier string, precision int32) (ZoneInterval, error) {
	overflow := false
	bad := func() (ZoneInterval, error) {
		if overflow {
			return ZoneInterval{}, failure("22015", fmt.Sprintf("interval field value out of range: \"%s\"", text))
		}
		return ZoneInterval{}, failure("22007", fmt.Sprintf("invalid input syntax for type interval: \"%s\"", text))
	}
	s := strings.TrimSpace(text)
	qualifier = strings.ToUpper(qualifier)
	if s == "" {
		return bad()
	}
	if strings.EqualFold(s, "infinity") {
		return ZoneInterval{Months: math.MaxInt32, Days: math.MaxInt32, Microseconds: math.MaxInt64}, nil
	}
	if strings.EqualFold(s, "-infinity") {
		return ZoneInterval{Months: math.MinInt32, Days: math.MinInt32, Microseconds: math.MinInt64}, nil
	}
	var result ZoneInterval
	negate := false
	if strings.HasPrefix(s, "@") {
		s = strings.TrimSpace(s[1:])
	}
	if strings.HasSuffix(strings.ToLower(s), " ago") {
		s = strings.TrimSpace(s[:len(s)-4])
		negate = true
	}
	isoSyntax := strings.HasPrefix(strings.TrimSpace(text), "P")
	if isoSyntax && negate {
		return bad()
	}
	seen := uint32(0)
	mark := func(mask uint32) bool {
		if seen&mask != 0 {
			return false
		}
		seen |= mask
		return true
	}
	addIntegerMicros := func(value int64) bool {
		if value > 0 && result.Microseconds > math.MaxInt64-value || value < 0 && result.Microseconds < math.MinInt64-value {
			overflow = true
			return false
		}
		result.Microseconds += value
		return true
	}
	addMicros := func(n float64) bool {
		n = math.RoundToEven(n)
		if math.IsNaN(n) || math.IsInf(n, 0) || n >= float64(math.MaxInt64) || n < float64(math.MinInt64) {
			overflow = true
			return false
		}
		return addIntegerMicros(int64(n))
	}
	addDays := func(n float64) bool {
		whole := math.Trunc(n)
		if math.IsNaN(n) || math.IsInf(n, 0) || whole < math.MinInt32 || whole > math.MaxInt32 {
			overflow = true
			return false
		}
		sum := result.Days + int64(whole)
		if sum < math.MinInt32 || sum > math.MaxInt32 {
			overflow = true
			return false
		}
		result.Days = sum
		return addMicros((n - whole) * float64(microsPerDay))
	}
	addMonths := func(n float64) bool {
		whole := math.Trunc(n)
		if math.IsNaN(n) || math.IsInf(n, 0) || whole < math.MinInt32 || whole > math.MaxInt32 {
			overflow = true
			return false
		}
		result.Months += int64(whole)
		return addDays((n - whole) * 30)
	}
	addYears := func(n, scale float64) bool {
		whole := math.Trunc(n)
		return addMonths(whole*scale*12 + math.RoundToEven((n-whole)*scale*12))
	}
	add := func(n float64, unit, number string) bool {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		kind := decodeZoneIntervalUnit(unit)
		if kind == zoneIntervalUnknown {
			return false
		}
		switch kind {
		case zoneIntervalMillennium:
			if !addYears(n, 1000) {
				return false
			}
		case zoneIntervalMonth:
			if !addMonths(n) {
				return false
			}
		case zoneIntervalCentury:
			if !addYears(n, 100) {
				return false
			}
		case zoneIntervalDecade:
			if !addYears(n, 10) {
				return false
			}
		case zoneIntervalYear:
			if !addYears(n, 1) {
				return false
			}
		case zoneIntervalWeek:
			if !addDays(n * 7) {
				return false
			}
		case zoneIntervalDay:
			if !addDays(n) {
				return false
			}
		case zoneIntervalHour, zoneIntervalMinute, zoneIntervalSecond, zoneIntervalMillisecond, zoneIntervalMicrosecond:
			scale := int64(1)
			switch kind {
			case zoneIntervalHour:
				scale = 3600000000
			case zoneIntervalMinute:
				scale = 60000000
			case zoneIntervalSecond:
				scale = 1000000
			case zoneIntervalMillisecond:
				scale = 1000
			}
			value, ok := zoneIntervalNumberMicros(number, scale)
			if !ok {
				overflow = true
				return false
			}
			if !addIntegerMicros(value) {
				return false
			}
		default:
			return false
		}
		if result.Months < math.MinInt32 || result.Months > math.MaxInt32 || result.Days < math.MinInt32 || result.Days > math.MaxInt32 {
			overflow = true
			return false
		}
		if !isoSyntax && !mark(zoneIntervalFieldMask(kind, n)) {
			return false
		}
		return true
	}
	if isoSyntax {
		if len(s) < 2 {
			return bad()
		}
		iso := s[1:]
		timePart := false
		if alternative, matched, err := decodeAlternativeZoneInterval(iso, qualifier, precision); matched {
			if err != nil {
				if coded, ok := err.(interface{ SQLState() string }); ok {
					if coded.SQLState() == "22008" {
						return ZoneInterval{}, err
					}
					overflow = coded.SQLState() == "22015"
				}
				return bad()
			}
			if negate {
				alternative.Months = -alternative.Months
				alternative.Days = -alternative.Days
				alternative.Microseconds = -alternative.Microseconds
			}
			return alternative, nil
		}
		for len(iso) > 0 {
			if iso[0] == 'T' {
				timePart = true
				iso = iso[1:]
				continue
			}
			i := 0
			for i < len(iso) && (asciiDigit(iso[i]) || iso[i] == '.' || iso[i] == '-' || iso[i] == '+') {
				i++
			}
			if i == 0 || i == len(iso) {
				return bad()
			}
			n, err := strconv.ParseFloat(iso[:i], 64)
			if err != nil {
				overflow = zoneIntervalNumberOverflow(err)
				return bad()
			}
			unit := ""
			if timePart {
				switch iso[i] {
				case 'H':
					unit = "hour"
				case 'M':
					unit = "minute"
				case 'S':
					unit = "second"
				default:
					return bad()
				}
			} else {
				switch iso[i] {
				case 'Y':
					unit = "year"
				case 'M':
					unit = "month"
				case 'W':
					unit = "week"
				case 'D':
					unit = "day"
				default:
					return bad()
				}
			}
			if !add(n, unit, iso[:i]) {
				return bad()
			}
			iso = iso[i+1:]
		}
	} else {
		rawFields := strings.Fields(s)
		type intervalField struct{ word, unit string }
		fields := make([]intervalField, 0, len(rawFields))
		for i := 0; i < len(rawFields); i++ {
			word, unit := rawFields[i], ""
			if split := strings.IndexFunc(word, unicode.IsLetter); split >= 0 {
				unit, word = word[split:], word[:split]
			}
			if unit == "" && i+1 < len(rawFields) && strings.IndexFunc(rawFields[i+1], unicode.IsLetter) == 0 {
				unit = rawFields[i+1]
				i++
			}
			fields = append(fields, intervalField{word: word, unit: unit})
		}
		// PostgreSQL decodes traditional fields right-to-left, including checked
		// accumulation and the implicit day preceding an hour/clock field.
		for i := len(fields) - 1; i >= 0; i-- {
			word, unit := fields[i].word, fields[i].unit
			if word == "" || unit != "" && decodeZoneIntervalUnit(unit) == zoneIntervalUnknown {
				return bad()
			}
			if dash := strings.IndexByte(strings.TrimLeft(word, "+-"), '-'); dash >= 0 && !strings.Contains(word, ":") {
				sign := int64(1)
				body := word
				if body[0] == '-' {
					sign = -1
					body = body[1:]
				} else if body[0] == '+' {
					body = body[1:]
				}
				parts := strings.Split(body, "-")
				if len(parts) != 2 {
					return bad()
				}
				years, err := strconv.ParseInt(parts[0], 10, 32)
				if err != nil {
					overflow = zoneIntervalNumberOverflow(err)
					return bad()
				}
				months, err := strconv.ParseInt(parts[1], 10, 32)
				if err != nil {
					overflow = zoneIntervalNumberOverflow(err)
					return bad()
				}
				if months < 0 || months > 11 {
					overflow = true
					return bad()
				}
				result.Months += sign * (years*12 + months)
				if result.Months < math.MinInt32 || result.Months > math.MaxInt32 {
					overflow = true
					return bad()
				}
				if !mark(uint32(1) << zoneIntervalMonth) {
					return bad()
				}
				continue
			}
			if strings.Contains(word, ":") {
				sign := int64(1)
				if strings.IndexFunc(word, unicode.IsLetter) >= 0 {
					return bad()
				}
				if word[0] == '-' {
					sign = -1
					word = word[1:]
				} else if word[0] == '+' {
					word = word[1:]
				}
				parts := strings.Split(word, ":")
				if len(parts) < 2 || len(parts) > 3 {
					return bad()
				}
				h, err := strconv.ParseInt(parts[0], 10, 64)
				if err != nil {
					overflow = zoneIntervalNumberOverflow(err)
					return bad()
				}
				if h < 0 {
					return bad()
				}
				m := int64(0)
				seconds := float64(0)
				if len(parts) == 2 && strings.Contains(parts[1], ".") {
					seconds, err = strconv.ParseFloat(parts[1], 64)
					m, h = h, 0
				} else {
					m, err = strconv.ParseInt(parts[1], 10, 32)
					if len(parts) == 3 && err == nil {
						seconds, err = strconv.ParseFloat(parts[2], 64)
					}
				}
				if err != nil {
					overflow = zoneIntervalNumberOverflow(err)
					return bad()
				}
				if m < 0 || m > 59 || seconds < 0 || seconds >= 61 {
					overflow = true
					return bad()
				}
				if h > math.MaxInt32 {
					overflow = true
					return bad()
				}
				total := h*3600000000 + m*60000000 + int64(math.RoundToEven(seconds*1000000))
				if !addIntegerMicros(sign * total) {
					return bad()
				}
				if !mark(uint32(1)<<zoneIntervalHour | uint32(1)<<zoneIntervalMinute | uint32(1)<<zoneIntervalSecond | uint32(1)<<zoneIntervalMillisecond | uint32(1)<<zoneIntervalMicrosecond) {
					return bad()
				}
				continue
			}
			n, err := strconv.ParseFloat(word, 64)
			if err != nil {
				overflow = zoneIntervalNumberOverflow(err)
				return bad()
			}
			if unit == "" && i+1 < len(fields) && (strings.Contains(fields[i+1].word, ":") || decodeZoneIntervalUnit(fields[i+1].unit) == zoneIntervalHour) {
				unit = "day"
			}
			if unit == "" {
				unit = "second"
				if qualifier == "HOUR" {
					unit = "hour"
				}
				if qualifier == "MINUTE" || qualifier == "HOUR TO MINUTE" {
					unit = "minute"
				}
			}
			if !add(n, unit, word) {
				return bad()
			}
		}
	}
	if negate {
		if result.Months == math.MinInt32 || result.Days == math.MinInt32 || result.Microseconds == math.MinInt64 {
			overflow = true
			return bad()
		}
		result.Months = -result.Months
		result.Days = -result.Days
		result.Microseconds = -result.Microseconds
	}
	if qualifier == "HOUR" {
		result.Microseconds = result.Microseconds / 3600000000 * 3600000000
	}
	if qualifier == "MINUTE" || qualifier == "HOUR TO MINUTE" {
		result.Microseconds = result.Microseconds / 60000000 * 60000000
	}
	if precision > 6 {
		precision = 6
	}
	if precision >= 0 && precision < 6 {
		unit := int64(1)
		for p := precision; p < 6; p++ {
			unit *= 10
		}
		q, r := result.Microseconds/unit, result.Microseconds%unit
		if r >= unit/2 {
			q++
		} else if r <= -unit/2 {
			q--
		}
		if q > math.MaxInt64/unit || q < math.MinInt64/unit {
			return ZoneInterval{}, failure("22008", "interval out of range")
		}
		result.Microseconds = q * unit
	}
	return result, nil
}
func (v ZoneInterval) String() string {
	if v.Months == math.MaxInt32 && v.Days == math.MaxInt32 && v.Microseconds == math.MaxInt64 {
		return "infinity"
	}
	if v.Months == math.MinInt32 && v.Days == math.MinInt32 && v.Microseconds == math.MinInt64 {
		return "-infinity"
	}
	var words []string
	if v.Months != 0 {
		if years := v.Months / 12; years != 0 {
			label := "year"
			if years != 1 && years != -1 {
				label = "years"
			}
			words = append(words, fmt.Sprintf("%d %s", years, label))
		}
		if months := v.Months % 12; months != 0 {
			label := "mon"
			if months != 1 && months != -1 {
				label = "mons"
			}
			words = append(words, fmt.Sprintf("%d %s", months, label))
		}
	}
	if v.Days != 0 {
		label := "day"
		if v.Days != 1 && v.Days != -1 {
			label = "days"
		}
		sign := ""
		if v.Days > 0 && v.Months < 0 {
			sign = "+"
		}
		words = append(words, fmt.Sprintf("%s%d %s", sign, v.Days, label))
	}
	if v.Microseconds != 0 || len(words) == 0 {
		sign := ""
		n := uint64(v.Microseconds)
		if v.Microseconds < 0 {
			sign = "-"
			n = uint64(-(v.Microseconds + 1)) + 1
		}
		if n > 0 && v.Microseconds > 0 && (v.Months < 0 || v.Days < 0) {
			sign = "+"
		}
		seconds := n / 1000000
		clock := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
		if fraction := n % 1000000; fraction != 0 {
			clock += "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
		}
		words = append(words, sign+clock)
	}
	return strings.Join(words, " ")
}
