package sqltype

import "context"

// Lookup facts are execution-private values from the same installed provider
// used by ResolveZone; no caller can modify its cached immutable rule handles.
type TimezoneNameFact struct {
	Name, Abbrev string
	East         int32
	IsDST        bool
}

type TimezoneAbbrevFact struct {
	Abbrev string
	East   int32
	IsDST  bool
}

type timezoneViewAbbreviation struct {
	label string
	entry zoneAbbreviation
}

func VisitTimezoneNames(ctx context.Context, unixSecond int64, emit func(TimezoneNameFact) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	zones.once.Do(initializeZones)
	for _, name := range zones.names {
		if err := ctx.Err(); err != nil {
			return err
		}
		zone, err := ResolveZone(name)
		if err != nil {
			return err
		}
		t := zone.typeAt(unixSecond)
		// PostgreSQL's exported abbreviation is at most 31 bytes. Do not
		// manufacture a replacement for an unrepresentable provider entry.
		if len(t.abbreviation) > 31 {
			continue
		}
		if err := emit(TimezoneNameFact{name, t.abbreviation, t.east, t.daylight}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func timezoneViewLabel(label string) bool {
	if len(label) == 0 || len(label) > 31 {
		return false
	}
	for i := range len(label) {
		if label[i] < 'A' || label[i] > 'Z' {
			return false
		}
	}
	return true
}

func VisitTimezoneAbbreviations(ctx context.Context, env InputEnvironment, emit func(TimezoneAbbrevFact) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	zones.once.Do(initializeZones)
	second := env.TransactionStart.Unix()
	seen := make(map[string]bool)
	session := env.Zone
	if session == nil {
		session = FixedZone("UTC", 0)
	}
	publish := func(t zoneType) error {
		label := t.abbreviation
		if !timezoneViewLabel(label) || seen[label] {
			return nil
		}
		seen[label] = true
		matched, found := session.abbreviationTypeAt(label, second)
		if !found {
			return nil
		}
		return emit(TimezoneAbbrevFact{label, matched.east, matched.daylight})
	}
	if err := publish(session.types[session.defaultType]); err != nil {
		return err
	}
	for _, tr := range session.transitions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := publish(session.types[tr.kind]); err != nil {
			return err
		}
	}
	for _, record := range zones.abbrevEntries[env.TimezoneAbbreviations] {
		if err := ctx.Err(); err != nil {
			return err
		}
		label := record.label
		if seen[label] {
			continue
		}
		entry := record.entry
		t := zoneType{east: entry.east, daylight: entry.daylight}
		if entry.owner != "" {
			owner, err := ResolveZone(entry.owner)
			if err != nil {
				return err
			}
			var found bool
			t, found = owner.abbreviationTypeAt(label, second)
			if !found {
				t = owner.typeAt(second)
			}
		}
		if err := emit(TimezoneAbbrevFact{label, t.east, t.daylight}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
