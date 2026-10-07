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

// Civil decoders and conversion use the same pinned rules/Default authority.
func timestampAbbreviationKind(name string) byte {
	zones.once.Do(initializeZones)
	entry, ok := lookupZoneAbbreviation(name)
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
func inputAbbreviationKind(name string, session *Zone) byte {
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
	return timestampAbbreviationKind(name)
}
func lookupZoneAbbreviation(name string) (zoneAbbreviation, bool) {
	if len(name) > 255 {
		return zoneAbbreviation{}, false
	}
	var folded [255]byte
	for i := range len(name) {
		folded[i] = timestampASCIILower(name[i])
	}
	entry, ok := zones.abbreviations[string(folded[:len(name)])]
	return entry, ok
}
func timestampZoneKind(name string) byte {
	if kind := timestampAbbreviationKind(name); kind != timestampUnknownZone {
		return kind
	}
	if _, err := ResolveZone(name); err == nil {
		return timestampNamedZone
	}
	return timestampUnknownZone
}
func recognizedTimestampZone(name string) bool {
	return timestampZoneKind(name) != timestampUnknownZone
}
