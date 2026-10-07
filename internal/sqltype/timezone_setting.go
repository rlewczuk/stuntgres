package sqltype

import (
	"math"
	"strconv"
	"strings"
)

func ParseSessionZone(text string) (*Zone, string, error) {
	if len(text) >= 8 && strings.EqualFold(text[:8], "interval") {
		body := strings.TrimSpace(text[8:])
		if len(body) < 2 || body[0] != '\'' || body[len(body)-1] != '\'' {
			return nil, "", failure("22023", "invalid time zone interval")
		}
		interval, err := DecodeZoneInterval(body[1:len(body)-1], "", -1)
		if err != nil {
			return nil, "", err
		}
		if interval.Months != 0 {
			return nil, "Cannot specify months in time zone interval.", failure("22023", "invalid time zone interval")
		}
		if interval.Days != 0 {
			return nil, "Cannot specify days in time zone interval.", failure("22023", "invalid time zone interval")
		}
		seconds := interval.Microseconds / 1000000
		if seconds < -604799 || seconds > 604799 {
			return nil, "UTC timezone offset is out of range.", failure("22023", "invalid timezone offset")
		}
		return sessionFixedZone(int32(seconds)), "", nil
	}
	if hours, err := strconv.ParseFloat(strings.TrimLeft(text, " \t\r\n\f\v"), 64); err == nil {
		seconds := math.Trunc(hours * 3600)
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < -604799 || seconds > 604799 {
			return nil, "UTC timezone offset is out of range.", failure("22023", "invalid timezone offset")
		}
		return sessionFixedZone(int32(seconds)), "", nil
	}
	zone, err := ResolveZone(text)
	return zone, "", err
}
func sessionFixedZone(east int32) *Zone {
	iso := zoneOffsetOutput(east)
	opposite := "-"
	if east < 0 {
		opposite = "+"
	}
	name := "<" + iso + ">" + opposite + iso[1:]
	z := FixedZone(name, east)
	z.types[0].abbreviation = iso
	return z
}
