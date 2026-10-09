package sqltype

// Interval's formatting fields are synthetic, not validated civil dates. The
// release projects them through signed 32-bit date2j arithmetic, including its
// truncating division and wraparound. Real DATE/TIMESTAMP still use civilDay.
func datetimeIntervalJulian(year, month, day int) int32 {
	y, m, d := int32(year), int32(month), int32(day)
	if m > 2 {
		m++
		y += 4800
	} else {
		m += 13
		y += 4799
	}
	century := y / 100
	return y*365 - 32167 + y/4 - century + century/4 + 7834*m/256 + d
}
func datetimeIntervalISOStart(year int) int32 {
	fourth := datetimeIntervalJulian(year, 1, 4)
	weekday := fourth % 7
	if weekday < 0 {
		weekday += 7
	}
	return fourth - weekday
}
func datetimeIntervalISO(year, month, day int) (int, int, int) {
	current := datetimeIntervalJulian(year, month, day)
	start := datetimeIntervalISOStart(year)
	isoYear := year
	if current < start {
		isoYear--
		start = datetimeIntervalISOStart(isoYear)
	}
	week := int((current-start)/7 + 1)
	// date2isoweek retains the original Gregorian year for its final check.
	if week >= 52 {
		next := datetimeIntervalISOStart(year + 1)
		if current >= next {
			week = int((current-next)/7 + 1)
		}
	}
	// date2isoyear instead carries its adjusted year into the same check.
	if (current-start)/7+1 >= 52 {
		next := datetimeIntervalISOStart(isoYear + 1)
		if current >= next {
			isoYear++
		}
	}
	yearday := current - datetimeIntervalISOStart(isoYear) + 1
	return isoYear, week, int(yearday)
}
