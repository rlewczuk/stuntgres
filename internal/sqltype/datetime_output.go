package sqltype

import "fmt"

func dateStyledOutput(day int64, style DateStyle) string {
	if day == DateNegativeInfinity || day == DatePositiveInfinity {
		return dateOutput(day)
	}
	y, m, d := dayCivil(day)
	bc := y <= 0
	if bc {
		y = 1 - y
	}
	text := formattedDate(y, m, d, style)
	if bc {
		text += " BC"
	}
	return text
}
func formattedDate(y, m, d int, style DateStyle) string {
	switch style.Style {
	case DateSQL:
		if style.Order == DateDMY {
			return fmt.Sprintf("%02d/%02d/%04d", d, m, y)
		}
		return fmt.Sprintf("%02d/%02d/%04d", m, d, y)
	case DatePostgres:
		if style.Order == DateDMY {
			return fmt.Sprintf("%02d-%02d-%04d", d, m, y)
		}
		return fmt.Sprintf("%02d-%02d-%04d", m, d, y)
	case DateGerman:
		return fmt.Sprintf("%02d.%02d.%04d", d, m, y)
	default:
		return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
	}
}
func timestampStyledOutput(n int64, style DateStyle, zone string, east int32, zoned bool) string {
	if n == TimestampNegativeInfinity || n == TimestampPositiveInfinity {
		return timestampOutput(n)
	}
	day := floorDiv(n, microsPerDay)
	clock := n - day*microsPerDay
	y, m, d := dayCivil(day)
	bc := y <= 0
	if bc {
		y = 1 - y
	}
	time := timeOutput(clock)
	var text string
	if style.Style == DatePostgres {
		weekday := int((day - unixEpochCivilDay + 4) % 7)
		if weekday < 0 {
			weekday += 7
		}
		week := [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[weekday]
		month := [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}[m-1]
		if style.Order == DateDMY {
			text = fmt.Sprintf("%s %02d %s %s %04d", week, d, month, time, y)
		} else {
			text = fmt.Sprintf("%s %s %02d %s %04d", week, month, d, time, y)
		}
	} else {
		text = formattedDate(y, m, d, style) + " " + time
	}
	if zoned {
		if style.Style == DateISO {
			text += zoneOffsetOutput(east)
		} else {
			if len(zone) > 10 {
				zone = zone[:10]
			}
			text += " " + zone
		}
	}
	if bc {
		text += " BC"
	}
	return text
}
