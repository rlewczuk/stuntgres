package sqltype

import "strings"

type zoneIntervalUnit uint8

const (
	zoneIntervalUnknown zoneIntervalUnit = iota
	zoneIntervalMillennium
	zoneIntervalCentury
	zoneIntervalDecade
	zoneIntervalYear
	zoneIntervalMonth
	zoneIntervalWeek
	zoneIntervalDay
	zoneIntervalHour
	zoneIntervalMinute
	zoneIntervalSecond
	zoneIntervalMillisecond
	zoneIntervalMicrosecond
)

func decodeZoneIntervalUnit(unit string) zoneIntervalUnit {
	// REL_18_0 DecodeUnits/datebsearch compare TOKMAXLEN (10) bytes.
	// This also admits full microsecond/millisecond spellings and plurals.
	if len(unit) > 10 {
		unit = unit[:10]
	}
	switch strings.ToLower(unit) {
	case "millennium", "millennia", "mil", "mils":
		return zoneIntervalMillennium
	case "century", "centuries", "c", "cent":
		return zoneIntervalCentury
	case "decade", "decades", "dec", "decs":
		return zoneIntervalDecade
	case "year", "years", "y", "yr", "yrs":
		return zoneIntervalYear
	case "month", "months", "mon", "mons":
		return zoneIntervalMonth
	case "week", "weeks", "w":
		return zoneIntervalWeek
	case "day", "days", "d":
		return zoneIntervalDay
	case "hour", "hours", "h", "hr", "hrs":
		return zoneIntervalHour
	case "minute", "minutes", "min", "mins", "m":
		return zoneIntervalMinute
	case "second", "seconds", "sec", "secs", "s":
		return zoneIntervalSecond
	case "millisecon", "msec", "msecs", "ms", "msecond", "mseconds":
		return zoneIntervalMillisecond
	case "microsecon", "usec", "usecs", "us", "usecond", "useconds":
		return zoneIntervalMicrosecond
	}
	return zoneIntervalUnknown
}
func zoneIntervalFieldMask(unit zoneIntervalUnit, n float64) uint32 {
	mask := uint32(1) << unit
	if unit == zoneIntervalSecond && n != float64(int64(n)) {
		mask |= uint32(1)<<zoneIntervalMillisecond | uint32(1)<<zoneIntervalMicrosecond
	}
	return mask
}
