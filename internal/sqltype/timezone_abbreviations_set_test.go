package sqltype

import (
	"sync"
	"testing"
)

func TestTimezoneAbbreviationsStockSets(t *testing.T) {
	for _, tc := range []struct {
		name          string
		set           TimezoneAbbreviations
		ist, est, cst int32
	}{
		{"Default", TimezoneAbbreviationsDefault, 7200, -18000, -21600},
		{"Australia", TimezoneAbbreviationsAustralia, 7200, 36000, 34200},
		{"India", TimezoneAbbreviationsIndia, 19800, -18000, -21600},
	} {
		set, ok := ParseTimezoneAbbreviations(tc.name)
		if !ok || set != tc.set || set.String() != tc.name {
			t.Fatal(tc.name, set, ok)
		}
		for name, want := range map[string]int32{"iSt": tc.ist, "EST": tc.est, "CST": tc.cst, "UTC": 0} {
			entry, ok := lookupZoneAbbreviation(name, set)
			if !ok || entry.east != want || entry.owner != "" {
				t.Fatal(tc.name, name, entry, want)
			}
		}
		entry, ok := lookupZoneAbbreviation("EDT", set)
		if !ok || entry.east != -14400 || !entry.daylight {
			t.Fatal(tc.name, entry)
		}
		entry, ok = lookupZoneAbbreviation("MSK", set)
		if !ok || entry.owner != "Europe/Moscow" {
			t.Fatal(tc.name, entry)
		}
	}
	for _, name := range []string{"default", "INDIA", " Australia", "Australia.txt", "../Default", "Default1", "Indiä"} {
		if _, ok := ParseTimezoneAbbreviations(name); ok {
			t.Fatal(name)
		}
	}
	if _, ok := lookupZoneAbbreviation("SAT", TimezoneAbbreviationsDefault); ok {
		t.Fatal("Default SAT must remain weekday")
	}
	entry, ok := lookupZoneAbbreviation("SAT", TimezoneAbbreviationsAustralia)
	if !ok || entry.east != 34200 {
		t.Fatal(entry, ok)
	}
}

func TestTimezoneAbbreviationsConcurrentPriority(t *testing.T) {
	zone, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	env := InputEnvironment{Zone: zone, TimezoneAbbreviations: TimezoneAbbreviationsAustralia}
	if inputAbbreviationKind("EST", env) == timestampUnknownZone {
		t.Fatal("session abbreviation not classified")
	}
	spec, err := resolveInputZone("EST", env)
	if err != nil || spec.offsetCivil(1088683200) != -18000 {
		t.Fatal(spec, err)
	}
	var group sync.WaitGroup
	for set := TimezoneAbbreviationsDefault; set <= TimezoneAbbreviationsIndia; set++ {
		group.Go(func() {
			want := int32(7200)
			if set == TimezoneAbbreviationsIndia {
				want = 19800
			}
			for range 100 {
				spec, err := resolveInputZone("IST", InputEnvironment{TimezoneAbbreviations: set})
				if err != nil || !spec.fixed || spec.east != want {
					t.Error(set, spec, err)
				}
			}
		})
	}
	group.Wait()
}
