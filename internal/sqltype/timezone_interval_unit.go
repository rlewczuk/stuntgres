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
	switch strings.ToLower(unit) {
	case "millennium", "millenniums", "millennia":
		return zoneIntervalMillennium
	case "century", "centuries":
		return zoneIntervalCentury
	case "decade", "decades":
		return zoneIntervalDecade
	case "year", "years", "y":
		return zoneIntervalYear
	case "month", "months", "mon", "mons":
		return zoneIntervalMonth
	case "week", "weeks", "w":
		return zoneIntervalWeek
	case "day", "days", "d":
		return zoneIntervalDay
	case "hour", "hours", "h":
		return zoneIntervalHour
	case "minute", "minutes", "min", "mins", "m":
		return zoneIntervalMinute
	case "second", "seconds", "sec", "secs", "s":
		return zoneIntervalSecond
	case "millisecond", "milliseconds", "msec", "msecs", "ms":
		return zoneIntervalMillisecond
	case "microsecond", "microseconds", "usec", "usecs", "us":
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
