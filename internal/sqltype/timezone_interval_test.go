package sqltype

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTimezoneIntervalNormalization(t *testing.T) {
	for _, tc := range []struct{ text, qualifier, want string }{
		{"-08:00", "HOUR TO MINUTE", "-08:00:00"},
		{"90", "MINUTE", "01:30:00"},
		{"90", "HOUR TO MINUTE", "01:30:00"},
		{"0.1 years", "", "1 mon"},
		{"1 2 hours", "", "1 day 02:00:00"},
		{"1.5 hours", "", "01:30:00"},
		{"@ 1.5 hours ago", "", "-01:30:00"},
		{"PT1H30M", "", "01:30:00"},
		{"PT1:90", "", "02:30:00"},
		{"P0000-00-00T05:30:00", "", "05:30:00"},
		{"P00000000T053000", "", "05:30:00"},
		{"1.5 months", "", "1 mon 15 days"},
		{"1 day", "HOUR", "1 day"},
		{"1 month -2 days 03:00", "", "1 mon -2 days +03:00:00"},
		{"1-2", "", "1 year 2 mons"},
		{"00:00:60", "", "00:01:00"},
		{"infinity", "", "infinity"},
	} {
		value, err := DecodeZoneInterval(tc.text, tc.qualifier, -1)
		if err != nil || value.String() != tc.want {
			t.Fatal(tc, value.String(), err)
		}
	}
	for _, tc := range []struct{ text, code string }{{"nonsense", "22007"}, {"00:60:00", "22015"}, {"9223372036854775807 hours", "22015"}, {"pT1H", "22007"}} {
		_, err := DecodeZoneInterval(tc.text, "", -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range []struct{ text, detail string }{{"1 month", "Cannot specify months in time zone interval."}, {"1 day", "Cannot specify days in time zone interval."}, {"168 hours", "UTC timezone offset is out of range."}} {
		_, detail, err := ParseSessionZone("INTERVAL '" + tc.text + "'")
		if err == nil || detail != tc.detail {
			t.Fatal(tc, detail, err)
		}
	}
	zone, _, err := ParseSessionZone("INTERVAL '-00:00:00.999999'")
	if err != nil {
		t.Fatal(err)
	}
	if east, _ := zone.OffsetAt(0); east != 0 {
		t.Fatal("fraction truncation", east)
	}
}
func TestTimezoneDateStyleStateAndLists(t *testing.T) {
	current := DateStyle{Style: DateSQL, Order: DateDMY}
	defaults := DateStyle{}
	for _, tc := range []struct {
		text string
		want DateStyle
	}{
		{"", current}, {"ISO", DateStyle{Style: DateISO, Order: DateDMY}},
		{"DEFAULT, SQL", DateStyle{Style: DateSQL, Order: DateMDY}},
		{"SQL, DEFAULT", DateStyle{Style: DateSQL, Order: DateMDY}},
		{`"ISO", "MDY"`, DateStyle{}},
		{"German, MDY, German", DateStyle{Style: DateGerman, Order: DateMDY}},
		{"PostgresAnything, Euro", DateStyle{Style: DatePostgres, Order: DateDMY}},
		{"NonEuro", DateStyle{Style: DateSQL, Order: DateMDY}},
	} {
		got, detail := ParseDateStyle(tc.text, current, defaults)
		if detail != "" || got != tc.want {
			t.Fatal(tc, got, detail)
		}
	}
	for _, tc := range []struct{ text, detail string }{
		{"ISO,,MDY", "List syntax is invalid."},
		{"ISO MDY", "List syntax is invalid."},
		{"ISO,SQL", "Conflicting \"DateStyle\" specifications."},
		{"ISO,SQL,nonsense", "Unrecognized key word: \"nonsense\"."},
	} {
		got, detail := ParseDateStyle(tc.text, current, defaults)
		if got != current || detail != tc.detail {
			t.Fatal(tc, got, detail)
		}
	}
}

func TestTimezoneIntervalExactArithmeticAndGrammar(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"9223372036854775807 microseconds", "2562047788:00:54.775807"},
		{"-9223372036854775808 microseconds", "-2562047788:00:54.775808"},
		{"1 2hours", "1 day 02:00:00"},
		{"PT1.5:0", "01:30:00"},
	} {
		got, err := DecodeZoneInterval(tc.text, "", -1)
		if err != nil || got.String() != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, text := range []string{"2 hours 3 hours", "P1H", "P1D ago", "@P1D", "-P1D"} {
		_, err := DecodeZoneInterval(text, "", -1)
		if err == nil {
			t.Fatal("accepted invalid interval", text)
		}
	}
	for _, text := range []string{"9223372036854775807 microseconds", "-9223372036854775808 microseconds"} {
		_, err := DecodeZoneInterval(text, "", 0)
		if err == nil || err.Error() != "stuntgres: interval out of range" {
			t.Fatal(text, err)
		}
	}
}
