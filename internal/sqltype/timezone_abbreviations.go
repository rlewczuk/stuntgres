package sqltype

import (
	"sort"
	"strings"
)

type zoneSpecification struct {
	zone         *Zone
	abbreviation string
	east         int32
	fixed        bool
}

func resolveParsedInputZone(name string, env InputEnvironment) (zoneSpecification, error) {
	// DecodeDateTime folds input tokens; AT TIME ZONE retains its expression text.
	specification, err := resolveInputZone(name, env)
	if err != nil {
		_, err = ResolveZone(strings.ToLower(name))
	}
	return specification, err
}

func resolveInputZone(name string, env InputEnvironment) (zoneSpecification, error) {
	zones.once.Do(initializeZones)
	session := env.Zone
	if session != nil {
		found := false
		var first zoneType
		dynamic := false
		for _, t := range session.types {
			if !strings.EqualFold(t.abbreviation, name) {
				continue
			}
			if !found {
				first = t
				found = true
			} else if first.east != t.east || first.daylight != t.daylight {
				dynamic = true
			}
		}
		if found {
			if !dynamic {
				return zoneSpecification{east: first.east, fixed: true}, nil
			}
			return zoneSpecification{zone: session, abbreviation: name}, nil
		}
	}
	if entry, ok := lookupZoneAbbreviation(name, env.TimezoneAbbreviations); ok {
		if entry.owner == "" {
			return zoneSpecification{east: entry.east, fixed: true}, nil
		}
		zone, err := ResolveZone(entry.owner)
		if err != nil {
			return zoneSpecification{}, err
		}
		return zoneSpecification{zone: zone, abbreviation: name}, nil
	}
	zone, err := ResolveZone(name)
	return zoneSpecification{zone: zone}, err
}
func (s zoneSpecification) offsetCivil(second int64) int32 {
	if s.fixed {
		return s.east
	}
	ordinary := s.zone.CivilOffset(second)
	if s.abbreviation == "" {
		return ordinary
	}
	return s.offsetInstant(second - int64(ordinary))
}
func (s zoneSpecification) offsetInstant(second int64) int32 {
	if s.fixed {
		return s.east
	}
	if s.abbreviation == "" {
		east, _ := s.zone.OffsetAt(second)
		return east
	}
	if t, found := s.zone.abbreviationTypeAt(s.abbreviation, second); found {
		return t.east
	}
	east, _ := s.zone.OffsetAt(second)
	return east
}

func (z *Zone) abbreviationTypeAt(abbreviation string, second int64) (zoneType, bool) {
	// PostgreSQL uses the latest matching interval at/before the probe, then
	// prehistory, then the first matching future interval, not current DST.
	cutoff := sort.Search(len(z.transitions), func(i int) bool { return z.transitions[i].second > second })
	for i := cutoff - 1; i >= 0; i-- {
		tr := z.transitions[i]
		t := z.types[tr.kind]
		if strings.EqualFold(t.abbreviation, abbreviation) {
			return t, true
		}
	}
	if t := z.types[z.defaultType]; strings.EqualFold(t.abbreviation, abbreviation) {
		return t, true
	}
	for _, tr := range z.transitions[cutoff:] {
		t := z.types[tr.kind]
		if strings.EqualFold(t.abbreviation, abbreviation) {
			return t, true
		}
	}
	return zoneType{}, false
}
