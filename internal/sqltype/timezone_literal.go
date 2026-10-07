package sqltype

import "fmt"

func timetzSQLLiteral(v Value) string {
	east := -v.west
	if east >= -57599 && east <= 57599 {
		return "'" + timeOutput(v.integer) + zoneOffsetOutput(east) + "'"
	}
	// The SQL literal displacement parser is deliberately narrower than SET's
	// provider offsets. A full POSIX name preserves the clock, including24:00.
	standard := east
	daylight := ""
	if east > 604800 {
		standard -= 3600
		daylight = "DST"
	}
	west := -standard
	sign := ""
	if west < 0 {
		sign = "-"
		west = -west
	}
	offset := fmt.Sprintf("%d:%02d:%02d", west/3600, west/60%60, west%60)
	if west == 604800 {
		offset = "167:59:60"
	}
	// July2000 selects the default POSIX northern-summer rule when an offset
	// above168h requires its default daylight adjustment. Date is discarded.
	return "'2000-07-01 " + timeOutput(v.integer) + " UTC" + sign + offset + daylight + "'"
}
