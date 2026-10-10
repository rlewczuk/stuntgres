package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestTimezoneViewNamesInstants(t *testing.T) {
	for _, tc := range []struct {
		zone, instant, abbrev string
		east                  int32
		dst                   bool
	}{
		{"America/New_York", "2024-03-10T06:59:59Z", "EST", -18000, false},
		{"America/New_York", "2024-03-10T07:00:00Z", "EDT", -14400, true},
		{"America/New_York", "2024-11-03T05:59:59Z", "EDT", -14400, true},
		{"America/New_York", "2024-11-03T06:00:00Z", "EST", -18000, false},
		{"Australia/Lord_Howe", "2024-04-06T14:59:59Z", "+11", 39600, true},
		{"Australia/Lord_Howe", "2024-04-06T15:00:00Z", "+1030", 37800, false},
		{"Europe/Dublin", "2024-01-15T00:00:00Z", "GMT", 0, true},
		{"Europe/Dublin", "2024-07-15T00:00:00Z", "IST", 3600, false},
		{"Europe/Amsterdam", "1800-01-01T00:00:00Z", "LMT", 1172, false},
		{"America/New_York", "2424-03-10T07:00:00Z", "EDT", -14400, true},
	} {
		t.Run(tc.zone+"/"+tc.instant, func(t *testing.T) {
			instant, err := time.Parse(time.RFC3339, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			var got TimezoneNameFact
			err = VisitTimezoneNames(context.Background(), instant.Unix(), func(f TimezoneNameFact) error {
				if f.Name == tc.zone {
					got = f
				}
				return nil
			})
			want := TimezoneNameFact{tc.zone, tc.abbrev, tc.east, tc.dst}
			if err != nil || got != want {
				t.Fatalf("got %+v/%v want %+v", got, err, want)
			}
		})
	}
}

func TestTimezoneViewInventory(t *testing.T) {
	zones.once.Do(initializeZones)
	// Resolve accepts request spellings and POSIX expressions; neither adds
	// archive members or changes enumeration multiplicity.
	for _, name := range []string{"utc", ":UTC", "ABC3DEF,M3.2.0,M11.1.0"} {
		if _, err := ResolveZone(name); err != nil {
			t.Fatal(err)
		}
	}
	want := make(map[string]int)
	for _, file := range zones.archive.File {
		want[file.Name]++
	}
	got := make(map[string]int)
	if err := VisitTimezoneNames(context.Background(), 0, func(f TimezoneNameFact) error { got[f.Name]++; return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("provider enumeration differs from canonical installed archive", got, want)
	}
	for name, count := range got {
		if count != 1 {
			t.Fatalf("duplicate provider name %s: %d", name, count)
		}
	}
}

func TestTimezoneViewAbbreviationHistory(t *testing.T) {
	for _, tc := range []struct {
		instant string
		east    int32
	}{
		{"2010-07-01T00:00:00Z", 10800}, {"2012-07-01T00:00:00Z", 14400}, {"2015-07-01T00:00:00Z", 10800},
	} {
		instant, err := time.Parse(time.RFC3339, tc.instant)
		if err != nil {
			t.Fatal(err)
		}
		env := InputEnvironment{TransactionStart: instant}
		var got TimezoneAbbrevFact
		if err := VisitTimezoneAbbreviations(context.Background(), env, func(f TimezoneAbbrevFact) error {
			if f.Abbrev == "MSK" {
				got = f
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got != (TimezoneAbbrevFact{"MSK", tc.east, false}) {
			t.Fatalf("%s got %+v", tc.instant, got)
		}
	}
	zone := &Zone{types: []zoneType{{abbreviation: "AAA", east: 123}, {abbreviation: "UNUSED", east: 456}, {abbreviation: "AAA", east: 123}}, defaultType: 0}
	seen := make(map[string]int)
	if err := VisitTimezoneAbbreviations(context.Background(), InputEnvironment{Zone: zone, TransactionStart: time.Unix(0, 0)}, func(f TimezoneAbbrevFact) error { seen[f.Abbrev]++; return nil }); err != nil {
		t.Fatal(err)
	}
	if seen["AAA"] != 1 || seen["UNUSED"] != 0 {
		t.Fatal(seen)
	}
	if _, found := zone.abbreviationTypeAt("UNUSED", 0); found {
		t.Fatal("unused type interpreted as used")
	}
	if _, found := zone.abbreviationTypeAt("MISSING", 0); found {
		t.Fatal("missing label interpreted")
	}
	for _, label := range []string{"+05", "-03", "mixed", "", "ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFG"} {
		if timezoneViewLabel(label) {
			t.Fatal("unsupported session abbreviation", label)
		}
	}
}

func TestTimezoneViewCancellationOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var retained TimezoneNameFact
	calls := 0
	err := VisitTimezoneNames(ctx, 0, func(f TimezoneNameFact) error { retained = f; calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err, calls)
	}
	before := retained
	if err := VisitTimezoneNames(context.Background(), 2000000000, func(TimezoneNameFact) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if retained != before {
		t.Fatal("fact changed after subsequent projection")
	}
	if err := VisitTimezoneAbbreviations(ctx, InputEnvironment{}, func(TimezoneAbbrevFact) error { t.Fatal("precanceled visitor called back"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sentinel := errors.New("visitor failed")
	if err := VisitTimezoneNames(context.Background(), 0, func(TimezoneNameFact) error { return sentinel }); err != sentinel {
		t.Fatal(err)
	}
}

func TestTimezoneViewFixedDaylightAndHistoricalFallback(t *testing.T) {
	for _, month := range []time.Month{time.January, time.July} {
		facts := make(map[string]TimezoneAbbrevFact)
		env := InputEnvironment{TransactionStart: time.Date(2024, month, 15, 0, 0, 0, 0, time.UTC)}
		if err := VisitTimezoneAbbreviations(context.Background(), env, func(f TimezoneAbbrevFact) error { facts[f.Abbrev] = f; return nil }); err != nil {
			t.Fatal(err)
		}
		if facts["EST"] != (TimezoneAbbrevFact{"EST", -18000, false}) || facts["EDT"] != (TimezoneAbbrevFact{"EDT", -14400, true}) {
			t.Fatal(month, facts["EST"], facts["EDT"])
		}
	}
	zone := &Zone{
		types:       []zoneType{{abbreviation: "OLD", east: 123}, {abbreviation: "NEW", east: 456, daylight: true}},
		transitions: []zoneTransition{{second: 10, kind: 1}},
	}
	for _, tc := range []struct {
		label  string
		second int64
		east   int32
		dst    bool
	}{
		{"OLD", 20, 123, false}, // Matching prehistory, not current daylight.
		{"NEW", 0, 456, true},   // First matching future interval.
		{"NEW", 20, 456, true},  // Latest matching past interval.
	} {
		got, found := zone.abbreviationTypeAt(tc.label, tc.second)
		if !found || got.east != tc.east || got.daylight != tc.dst {
			t.Fatal(tc, got, found)
		}
	}
	specification := zoneSpecification{zone: zone, abbreviation: "MISSING"}
	if got := specification.offsetInstant(20); got != 456 {
		t.Fatal("missing label must use owner current offset", got)
	}
}
