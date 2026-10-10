package sqltype

import (
	"strings"
)

const (
	timestampUnknownZone byte = iota
	timestampFixedZone
	timestampDaylightZone
	timestampDynamicZone
	timestampNamedZone
)

// Civil decoders and conversion share the captured stock-table authority.
func timestampAbbreviationKind(name string, set TimezoneAbbreviations) byte {
	entry, ok := lookupZoneAbbreviation(name, set)
	if !ok {
		return timestampUnknownZone
	}
	if entry.owner != "" {
		return timestampDynamicZone
	}
	if entry.daylight {
		return timestampDaylightZone
	}
	return timestampFixedZone
}
func inputAbbreviationKind(name string, env InputEnvironment) byte {
	session := env.Zone
	if session != nil {
		var match zoneType
		found := false
		for _, kind := range session.types {
			if !strings.EqualFold(kind.abbreviation, name) {
				continue
			}
			if found && (match.east != kind.east || match.daylight != kind.daylight) {
				return timestampDynamicZone
			}
			found, match = true, kind
		}
		if found {
			if match.daylight {
				return timestampDaylightZone
			}
			return timestampFixedZone
		}
	}
	return timestampAbbreviationKind(name, env.TimezoneAbbreviations)
}
func lookupZoneAbbreviation(name string, set TimezoneAbbreviations) (zoneAbbreviation, bool) {
	zones.once.Do(initializeZones)
	if len(name) > 255 {
		return zoneAbbreviation{}, false
	}
	var folded [255]byte
	for i := range len(name) {
		folded[i] = timestampASCIILower(name[i])
	}
	entry, ok := zones.abbreviations[set][string(folded[:len(name)])]
	return entry, ok
}
func timestampZoneKind(name string, env InputEnvironment) byte {
	if kind := inputAbbreviationKind(name, env); kind != timestampUnknownZone {
		return kind
	}
	if _, err := ResolveZone(name); err == nil {
		return timestampNamedZone
	}
	return timestampUnknownZone
}
func recognizedTimestampZone(name string, env InputEnvironment) bool {
	return timestampZoneKind(name, env) != timestampUnknownZone
}
