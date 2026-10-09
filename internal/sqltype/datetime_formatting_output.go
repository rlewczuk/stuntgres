package sqltype

import (
	"context"
	"strconv"
	"strings"
)

var datetimeMonths = [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var datetimeDays = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var datetimeRomans = [12]string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII"}

func datetimeNumber(b *strings.Builder, n int64, width int, ordinal uint8) {
	var scratch [32]byte
	digits := strconv.AppendInt(scratch[:0], n, 10)
	if len(digits) > 0 && digits[0] == '-' {
		b.WriteByte('-')
		digits = digits[1:]
		width--
	}
	for i := len(digits); i < width; i++ {
		b.WriteByte('0')
	}
	b.Write(digits)
	if ordinal != 0 {
		if n < 0 {
			n = -n
		}
		suffix := "TH"
		if n%100 < 10 || n%100 > 19 {
			switch n % 10 {
			case 1:
				suffix = "ST"
			case 2:
				suffix = "ND"
			case 3:
				suffix = "RD"
			}
		}
		if ordinal == 2 {
			b.WriteByte(suffix[0] + 32)
			b.WriteByte(suffix[1] + 32)
		} else {
			b.WriteString(suffix)
		}
	}
}
func datetimeName(b *strings.Builder, s, keyword string, padding int) {
	lower := keyword[0] >= 'a' && keyword[0] <= 'z'
	upper := len(keyword) > 1 && keyword[1] >= 'A' && keyword[1] <= 'Z'
	for i := range len(s) {
		c := s[i]
		if lower && c >= 'A' && c <= 'Z' {
			c += 32
		} else if upper && c >= 'a' && c <= 'z' {
			c -= 32
		}
		b.WriteByte(c)
	}
	for i := len(s); i < padding; i++ {
		b.WriteByte(' ')
	}
}

func datetimeLocalizedOutputName(b *strings.Builder, name, keyword string, padding int, conflict bool) error {
	if conflict {
		operation := "lower()"
		if keyword[0] >= 'A' && keyword[0] <= 'Z' {
			operation = "upper()"
			if len(keyword) > 1 && keyword[1] >= 'a' && keyword[1] <= 'z' {
				operation = "initcap()"
			}
		}
		return datetimeCollationError(operation)
	}
	datetimeName(b, name, keyword, padding)
	return nil
}
func datetimeOutput(ctx context.Context, result *Descriptor, value Value, nodes []datetimeNode, template string, env OutputEnvironment, conflict bool) (Value, error) {
	if len(template) == 0 {
		return Null(result), nil
	}
	y, m, d, h, minute, second, micro, dayyear, weekday := 0, 0, 0, int64(0), 0, 0, 0, 0, 0
	interval := value.typ.id == Interval
	east := int32(0)
	zone := ""
	if interval {
		if intervalInfinity(value) != 0 {
			return Null(result), nil
		}
		y = int(value.months / 12)
		m = int(value.months % 12)
		d = int(value.days)
		h = value.integer / 3600000000
		minute = int(value.integer / 60000000 % 60)
		second = int(value.integer / 1000000 % 60)
		micro = int(value.integer % 1000000)
		dayyear = int(value.months*30 + value.days)
	} else {
		if value.integer == TimestampNegativeInfinity || value.integer == TimestampPositiveInfinity {
			return Null(result), nil
		}
		n := value.integer
		if value.typ.id == Timestamptz {
			east, zone = env.Zone.OffsetAt(floorDiv(n, 1000000) + 946684800)
			n += int64(east) * 1000000
		}
		days := floorDiv(n, microsPerDay)
		y, m, d = dayCivil(days)
		clock := n - days*microsPerDay
		h = clock / 3600000000
		minute = int(clock / 60000000 % 60)
		second = int(clock / 1000000 % 60)
		micro = int(clock % 1000000)
		dayyear = int(days-civilDay(y, 1, 1)) + 1
		weekday = int((days + 6) % 7)
		if weekday < 0 {
			weekday += 7
		}
	}
	isoYear, isoWeek, isoDay := 0, 0, 0
	isoReady := false
	adjusted := func(n int) int64 {
		if !interval && n <= 0 {
			return int64(1 - n)
		}
		return int64(n)
	}
	var b strings.Builder
	b.Grow(len(template))
	for i, node := range nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		if node.key == nil {
			b.WriteString(node.literal)
			continue
		}
		key := node.key.kind
		n := int64(0)
		width := 0
		ordinal := node.ordinal
		numeric := true
		if !isoReady && (key == "IYYY" || key == "IYY" || key == "IY" || key == "I" || key == "IW" || key == "IDDD") {
			if interval {
				isoYear, isoWeek, isoDay = datetimeIntervalISO(y, m, d)
			} else {
				isoYear, isoWeek, isoDay = datetimeISO(y, m, d)
			}
			isoReady = true
		}
		invalid := false
		switch key {
		case "FX":
			numeric = false
		case "HH", "HH12":
			n = h % 12
			if n == 0 {
				n = 12
			}
			width = 2
			if h < 0 {
				width++
			}
		case "HH24":
			n = h
			width = 2
			if h < 0 {
				width++
			}
		case "MI":
			n = int64(minute)
			width = 2
			if n < 0 {
				width++
			}
		case "SS":
			n = int64(second)
			width = 2
			if n < 0 {
				width++
			}
		case "SSSS":
			n = h*3600 + int64(minute*60+second)
		case "MS", "FF3":
			n = int64(micro / 1000)
			width = 3
		case "US", "FF6":
			n = int64(micro)
			width = 6
		case "FF1":
			n = int64(micro / 100000)
			width = 1
		case "FF2":
			n = int64(micro / 10000)
			width = 2
		case "FF4":
			n = int64(micro / 100)
			width = 4
		case "FF5":
			n = int64(micro / 10)
			width = 5
		case "AM", "PM", "am", "pm", "A_M", "P_M", "a_m", "p_m":
			numeric = false
			s := "AM"
			if h%24 >= 12 {
				s = "PM"
			}
			if key == "A_M" || key == "P_M" || key == "a_m" || key == "p_m" {
				s = "A.M."
				if h%24 >= 12 {
					s = "P.M."
				}
			}
			datetimeName(&b, s, node.key.word, 0)
		case "AD", "BC", "ad", "bc", "A_D", "B_C", "a_d", "b_c":
			invalid = interval
			numeric = false
			s := "AD"
			if y <= 0 {
				s = "BC"
			}
			if key == "A_D" || key == "B_C" || key == "a_d" || key == "b_c" {
				s = "A.D."
				if y <= 0 {
					s = "B.C."
				}
			}
			datetimeName(&b, s, node.key.word, 0)
		case "MONTH", "Month", "month", "MON", "Mon", "mon":
			invalid = interval
			numeric = false
			if invalid {
				break
			}
			if m > 0 && m <= 12 {
				s := datetimeMonths[m-1]
				pad := 0
				if len(node.key.word) == 3 {
					s = s[:3]
				} else if !node.fm && !node.tm {
					pad = 9
				}
				if err := datetimeLocalizedOutputName(&b, s, node.key.word, pad, node.tm && conflict); err != nil {
					return Value{}, err
				}
			}
		case "DAY", "Day", "day", "DY", "Dy", "dy":
			invalid = interval
			numeric = false
			if invalid {
				break
			}
			s := datetimeDays[weekday]
			pad := 0
			if len(node.key.word) == 2 {
				s = s[:3]
			} else if !node.fm && !node.tm {
				pad = 9
			}
			if err := datetimeLocalizedOutputName(&b, s, node.key.word, pad, node.tm && conflict); err != nil {
				return Value{}, err
			}
		case "MM":
			n = int64(m)
			width = 2
			if n < 0 {
				width++
			}
		case "DDD":
			n = int64(dayyear)
			width = 3
		case "IDDD":
			n = int64(isoDay)
			width = 3
		case "DD":
			n = int64(d)
			width = 2
		case "D":
			invalid = interval
			n = int64(weekday + 1)
		case "ID":
			invalid = interval
			n = int64(weekday)
			if n == 0 {
				n = 7
			}
		case "WW":
			n = int64((int32(dayyear)-1)/7 + 1)
			width = 2
		case "IW":
			n = int64(isoWeek)
			width = 2
		case "W":
			n = int64((int32(d)-1)/7 + 1)
		case "Q":
			if m == 0 {
				numeric = false
			} else {
				n = int64((m-1)/3 + 1)
			}
		case "CC":
			if interval {
				n = int64(y / 100)
			} else if y > 0 {
				n = int64((y-1)/100 + 1)
			} else {
				n = int64(y/100 - 1)
			}
			width = 2
			if n < 0 {
				width++
			}
		case "YYYY", "YYY", "YY", "Y", "IYYY", "IYY", "IY", "I":
			n = adjusted(y)
			if key[0] == 'I' {
				n = adjusted(isoYear)
			}
			switch key {
			case "YYYY", "IYYY":
				width = 4
			case "YYY", "IYY":
				n %= 1000
				width = 3
			case "YY", "IY":
				n %= 100
				width = 2
			default:
				n %= 10
				width = 1
			}
			if width > 1 && adjusted(y) < 0 {
				width++
			}
		case "Y_YYY":
			numeric = false
			n = adjusted(y)
			datetimeNumber(&b, n/1000, 0, 0)
			b.WriteByte(',')
			datetimeNumber(&b, n%1000, 3, ordinal)
		case "J":
			if interval {
				n = int64(datetimeIntervalJulian(y, m, d))
			} else {
				n = civilDay(y, m, d) + 2451545
			}
		case "RM", "rm":
			numeric = false
			if m == 0 && y == 0 {
				continue
			}
			month := m
			if month == 0 {
				if y >= 0 {
					month = 12
				} else {
					month = 1
				}
			} else if month < 0 {
				month = 13 + month
			}
			pad := 4
			if node.fm {
				pad = 0
			}
			if month >= 1 && month <= 12 {
				datetimeName(&b, datetimeRomans[month-1], node.key.word, pad)
			}
		case "TZ", "tz":
			invalid = interval
			numeric = false
			if key == "tz" {
				datetimeName(&b, zone, "tz", 0)
			} else {
				b.WriteString(zone)
			}
		case "TZH", "TZM", "OF":
			invalid = interval
			numeric = false
			offset := int64(east)
			sign := byte('+')
			if offset < 0 {
				offset = -offset
				sign = '-'
			}
			if key != "TZM" {
				b.WriteByte(sign)
				w := 2
				if key == "OF" && node.fm {
					w = 0
				}
				datetimeNumber(&b, offset/3600, w, 0)
			}
			if key == "TZM" {
				datetimeNumber(&b, offset%3600/60, 2, 0)
			} else if key == "OF" && offset%3600 != 0 {
				b.WriteByte(':')
				datetimeNumber(&b, offset%3600/60, 2, 0)
			}
		}
		if invalid {
			return Value{}, datetimeError("22007", "invalid format specification for an interval value", "", "Intervals are not tied to specific calendar dates.")
		}
		if numeric {
			if node.fm && key != "MS" && key != "US" && !strings.HasPrefix(key, "FF") {
				width = 0
			}
			datetimeNumber(&b, n, width, ordinal)
		}
	}
	return String(result, b.String()), nil
}
