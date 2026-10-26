package settings

import "math"

type unitEntry struct {
	name   string
	factor float64
}

var memoryUnits = []unitEntry{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"kB", 1 << 10}, {"B", 1}}
var timeUnits = []unitEntry{{"d", 86400000000}, {"h", 3600000000}, {"min", 60000000}, {"s", 1000000}, {"ms", 1000}, {"us", 1}}

func unitTable(u Unit) ([]unitEntry, float64, string) {
	switch u {
	case Bytes:
		return memoryUnits, 1, "B"
	case Kilobytes:
		return memoryUnits, 1024, "kB"
	case Megabytes:
		return memoryUnits, 1 << 20, "MB"
	case Blocks8kB:
		return memoryUnits, 8192, "8kB"
	case Milliseconds:
		return timeUnits, 1000, "ms"
	case Seconds:
		return timeUnits, 1000000, "s"
	case Minutes:
		return timeUnits, 60000000, "min"
	}
	return nil, 1, ""
}
func baseUnitLabel(u Unit) string {
	_, _, name := unitTable(u)
	if name != "" {
		return " " + name
	}
	return ""
}
func unitHint(u Unit) string {
	if u <= Megabytes || u == Blocks8kB {
		return `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`
	}
	return `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`
}
func convertUnit(n float64, suffix string, u Unit) (float64, bool) {
	table, base, _ := unitTable(u)
	if suffix == "" {
		return n, true
	}
	for i, unit := range table {
		if unit.name == suffix {
			n *= unit.factor / base
			if i+1 < len(table) {
				smaller := table[i+1].factor / base
				n = math.RoundToEven(n/smaller) * smaller
			}
			return n, true
		}
	}
	return 0, false
}
