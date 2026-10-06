package sqltype

import "sort"

func fixedTimeZone(s string) bool {
	i := sort.Search(len(timeFixedZoneNames), func(i int) bool {
		return timestampASCIICompare(s, timeFixedZoneNames[i]) <= 0
	})
	if i < len(timeFixedZoneNames) && timestampASCIIEqual(s, timeFixedZoneNames[i]) {
		return true
	}
	valid, fixed := posixTemporalZone(s)
	return valid && fixed
}

// Derived from PostgreSQL REL_18_0 tzdata.zi (IANA 2025b), compiled with
// zic -b fat without history truncation. Inspect every authoritative 64-bit
// ttinfo offset, including footer ttinfos admitted by REL_18_0 tzloadbody's
// abbreviation-capacity gate; pg_get_timezone_offset requires all to agree.
// Input SHA256: 786968ac34be38f7fd325b252848c25a2b2299f3ce44caea7659b0e8d492d72a.
// All 598 Zone/Link names were classified; these 45 are historically fixed.
// No host zone data, runtime file access or current-DST approximation is used.
var timeFixedZoneNames = [...]string{
	"etc/gmt", "etc/gmt+0", "etc/gmt+1", "etc/gmt+10", "etc/gmt+11", "etc/gmt+12",
	"etc/gmt+2", "etc/gmt+3", "etc/gmt+4", "etc/gmt+5", "etc/gmt+6", "etc/gmt+7", "etc/gmt+8", "etc/gmt+9",
	"etc/gmt-0", "etc/gmt-1", "etc/gmt-10", "etc/gmt-11", "etc/gmt-12", "etc/gmt-13", "etc/gmt-14",
	"etc/gmt-2", "etc/gmt-3", "etc/gmt-4", "etc/gmt-5", "etc/gmt-6", "etc/gmt-7", "etc/gmt-8", "etc/gmt-9",
	"etc/gmt0", "etc/greenwich", "etc/uct", "etc/universal", "etc/utc", "etc/zulu", "factory",
	"gmt", "gmt+0", "gmt-0", "gmt0", "greenwich", "uct", "universal", "utc", "zulu",
}
