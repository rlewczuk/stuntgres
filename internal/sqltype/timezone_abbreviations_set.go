package sqltype

import (
	_ "embed"
	"strconv"
	"strings"
)

// TimezoneAbbreviations selects an immutable embedded input table. Its zero
// value is PostgreSQL's Default set; it never selects a live global setting.
type TimezoneAbbreviations uint8

const (
	TimezoneAbbreviationsDefault TimezoneAbbreviations = iota
	TimezoneAbbreviationsAustralia
	TimezoneAbbreviationsIndia
)

func ParseTimezoneAbbreviations(name string) (TimezoneAbbreviations, bool) {
	switch name {
	case "Default":
		return TimezoneAbbreviationsDefault, true
	case "Australia":
		return TimezoneAbbreviationsAustralia, true
	case "India":
		return TimezoneAbbreviationsIndia, true
	default:
		return 0, false
	}
}

func (s TimezoneAbbreviations) String() string {
	switch s {
	case TimezoneAbbreviationsDefault:
		return "Default"
	case TimezoneAbbreviationsAustralia:
		return "Australia"
	case TimezoneAbbreviationsIndia:
		return "India"
	default:
		panic("invalid timezone abbreviation set")
	}
}

//go:embed tzdata/Australia
var australiaZoneAbbreviations string

//go:embed tzdata/India
var indiaZoneAbbreviations string

// Only the three checked-in stock assets are accepted here. Initialization
// validates directives/duplicates instead of silently ignoring corrupt data.
func stockAbbreviations(source string, defaults map[string]zoneAbbreviation) map[string]zoneAbbreviation {
	table := make(map[string]zoneAbbreviation)
	override, included := false, false
	for _, line := range strings.Split(source, "\n") {
		if p := strings.IndexByte(line, '#'); p >= 0 {
			line = line[:p]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "@INCLUDE":
			if len(fields) != 2 || fields[1] != "Default" || defaults == nil || included || len(table) != 0 {
				panic("corrupt embedded abbreviation include")
			}
			for name, entry := range defaults {
				table[name] = entry
			}
			included = true
			continue
		case "@OVERRIDE":
			if len(fields) != 1 || !included || override {
				panic("corrupt embedded abbreviation override")
			}
			override = true
			continue
		}
		if len(fields) < 2 || len(fields) > 3 || strings.HasPrefix(fields[0], "@") {
			panic("corrupt embedded abbreviation record")
		}
		name := strings.ToLower(fields[0])
		if _, exists := table[name]; exists && !override {
			panic("duplicate embedded abbreviation")
		}
		entry := zoneAbbreviation{}
		if east, err := strconv.ParseInt(fields[1], 10, 32); err == nil {
			entry.east = int32(east)
			if len(fields) == 3 {
				if fields[2] != "D" {
					panic("corrupt embedded abbreviation daylight flag")
				}
				entry.daylight = true
			}
		} else {
			if len(fields) != 2 {
				panic("corrupt embedded dynamic abbreviation")
			}
			entry.owner = fields[1]
		}
		table[name] = entry
	}
	if defaults != nil && (!included || !override) {
		panic("missing embedded abbreviation directives")
	}
	return table
}
