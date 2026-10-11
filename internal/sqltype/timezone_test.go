package sqltype

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func timezoneEnvironment(t *testing.T, name string, clock time.Time) InputEnvironment {
	t.Helper()
	zone, err := ResolveZone(name)
	if err != nil {
		t.Fatal(err)
	}
	return InputEnvironment{Zone: zone, TransactionStart: clock}
}
func TestTimezonePinnedInputAndTransitions(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{ zone, input, want string }{
		{"UTC", "2004-10-19 10:23:54+02", "2004-10-19 08:23:54+00"},
		{"America/New_York", "2004-07-01 12:00", "2004-07-01 16:00:00+00"},
		{"America/New_York", "2018-03-11 02:30", "2018-03-11 07:30:00+00"},
		{"America/New_York", "2018-11-04 01:30", "2018-11-04 06:30:00+00"},
		{"UTC", "2018-11-04 01:30 EDT", "2018-11-04 05:30:00+00"},
		{"UTC", "1800-01-01 12:00 Europe/Amsterdam", "1800-01-01 11:40:28+00"},
		{"Asia/Shanghai", "2000-01-01 12:00 CST", "2000-01-01 04:00:00+00"},
		{"UTC", "2000-01-01 12:00 CST", "2000-01-01 18:00:00+00"},
		{"Asia/Kolkata", "2000-01-01 12:00 IST", "2000-01-01 06:30:00+00"},
		{"UTC", "2000-01-01 12:00 IST", "2000-01-01 10:00:00+00"},
		{"America/New_York", "2000-01-01 12:00 LMT", "2000-01-01 16:56:02+00"},
		{"UTC", "2010-07-01 12:00 MSK", "2010-07-01 09:00:00+00"},
		{"UTC", "2012-07-01 12:00 MSK", "2012-07-01 08:00:00+00"},
		{"UTC", "2015-07-01 12:00 MSK", "2015-07-01 09:00:00+00"},
		{"Australia/Lord_Howe", "2020-10-04 02:15", "2020-10-03 15:45:00+00"},
		{"Australia/Lord_Howe", "2020-04-05 01:45", "2020-04-04 15:15:00+00"},
	} {
		env := timezoneEnvironment(t, tc.zone, time.Date(2020, 7, 1, 0, 0, 0, 0, time.UTC))
		v, err := types[Timestamptz].InputWithEnvironment(ctx, tc.input, -1, env)
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := types[Timestamptz].Output(ctx, v)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
func TestTimezoneTimetzClockAndKeys(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Timetz]
	source, err := d.Input(ctx, "05:34:17-05", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		month time.Month
		want  string
	}{{time.January, "05:34:17-05"}, {time.July, "06:34:17-04"}} {
		env := timezoneEnvironment(t, "UTC", time.Date(2020, tc.month, 1, 12, 0, 0, 0, time.UTC))
		for range 3 {
			v, err := AtTimeZone(source, d, "America/New_York", false, env)
			got, outputErr := d.Output(ctx, v)
			if err != nil || outputErr != nil || got != tc.want {
				t.Fatal(tc, got, err, outputErr)
			}
		}
	}
	a, _ := d.Input(ctx, "09:00+01", -1)
	b, _ := d.Input(ctx, "08:00+00", -1)
	comparison, _ := BindComparison(d, d, CollationRef{}, false)
	n, err := comparison.Compare(ctx, a, b, InputEnvironment{})
	if err != nil || n != -1 || SameDatum(a, b) {
		t.Fatal(n, err)
	}
	grouping, _ := BindGrouping(d.Usage())
	equal, err := grouping.Equal(ctx, a, b)
	ah, _ := grouping.Hash(ctx, a)
	bh, _ := grouping.Hash(ctx, b)
	if err != nil || equal || ah == bh {
		t.Fatal(equal, ah, bh, err)
	}
	again, _ := d.Input(ctx, "09:00+01", -1)
	equal, err = grouping.Equal(ctx, a, again)
	ch, _ := grouping.Hash(ctx, again)
	if err != nil || !equal || ah != ch {
		t.Fatal(equal, ah, ch, err)
	}
}
func TestTimezoneCrossComparisonAndStyles(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	date, _ := types[Date].Input(ctx, "2004-10-19", -1)
	instant, _ := types[Timestamptz].Input(ctx, "2004-10-19 04:00+00", -1)
	comparison, _ := BindComparison(types[Date], types[Timestamptz], CollationRef{}, false)
	if !comparison.SessionDependent() {
		t.Fatal("cross instant comparison must be Stable")
	}
	for _, tc := range []struct {
		zone string
		want int
	}{{"UTC", -1}, {"America/New_York", 0}} {
		got, err := comparison.Compare(ctx, date, instant, timezoneEnvironment(t, tc.zone, time.Time{}))
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		order DateOrder
		want  string
	}{{DateMDY, "2003-01-02"}, {DateDMY, "2003-02-01"}, {DateYMD, "2001-02-03"}} {
		v, err := types[Date].InputWithEnvironment(ctx, "01/02/03", -1, InputEnvironment{DateStyle: DateStyle{Order: tc.order}})
		got, outputErr := types[Date].Output(ctx, v)
		if err != nil || outputErr != nil || got != tc.want {
			t.Fatal(tc, got, err, outputErr)
		}
	}
	for _, tc := range []struct{ setting, want string }{
		{"SQL, MDY", "12/17/1997 07:37:16"}, {"SQL, DMY", "17/12/1997 07:37:16"},
		{"German", "17.12.1997 07:37:16"}, {"Postgres, MDY", "Wed Dec 17 07:37:16 1997"},
		{"Postgres, DMY", "Wed 17 Dec 07:37:16 1997"},
	} {
		style, detail := ParseDateStyle(tc.setting, DateStyle{}, DateStyle{})
		if detail != "" {
			t.Fatal(detail)
		}
		v, _ := types[Timestamp].Input(ctx, "1997-12-17 07:37:16", -1)
		got, err := types[Timestamp].OutputWithEnvironment(ctx, v, OutputEnvironment{DateStyle: style})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	style, detail := ParseDateStyle("DEFAULT, SQL", DateStyle{Style: DateGerman, Order: DateDMY}, DateStyle{})
	if detail != "" || style != (DateStyle{Style: DateSQL, Order: DateMDY}) {
		t.Fatal(style, detail)
	}
}
func TestTimezonePOSIXBoundsAndExtremeReplay(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		name string
		east int32
	}{{"UTC0:00:60", -60}, {"UTC167:59:60", -604800}, {"UTC-167:59:60DST", 608400}} {
		zone, err := ResolveZone(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		probe := time.Date(2000, 7, 1, 12, 0, 0, 0, time.UTC)
		east, _ := zone.OffsetAt(probe.Unix())
		if east != tc.east {
			t.Fatal(tc, east)
		}
		env := InputEnvironment{Zone: zone, TransactionStart: probe}
		for _, clock := range []string{"12:00", "24:00"} {
			v, err := types[Timetz].InputWithEnvironment(ctx, clock, -1, env)
			if err != nil {
				t.Fatal(tc, err)
			}
			n, _ := v.Time()
			west, _ := v.TimeZoneOffset()
			frozen, err := TimetzValue(types[Timetz], n, west)
			if err != nil || !SameDatum(v, frozen) {
				t.Fatal(err)
			}
			literal, err := types[Timetz].SQLLiteral(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := types[Timetz].InputWithEnvironment(ctx, strings.Trim(literal, "'"), -1, InputEnvironment{})
			if err != nil || !SameDatum(v, replayed) {
				t.Fatal(tc, clock, literal, replayed, err)
			}
		}
	}
	_, _, err := ParseSessionZone("168")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" {
		t.Fatal(err)
	}
	_, err = types[Timestamptz].Input(ctx, "2000-01-01 12:00+00:00:60", -1)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22009" {
		t.Fatal(err)
	}
}
func TestTimezonePinnedNamesAndFullRange(t *testing.T) {
	zones.once.Do(initializeZones)
	for name := range zones.members {
		if _, err := ResolveZone(name); err != nil {
			t.Fatal(name, err)
		}
	}
	zone, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, year := range []int{2400, 12000, 294276} {
		july := (civilDay(year, 7, 1)-unixEpochCivilDay)*86400 + 43200
		january := (civilDay(year, 1, 1)-unixEpochCivilDay)*86400 + 43200
		if got := zone.CivilOffset(july); got != -14400 {
			t.Fatal(year, "July", got)
		}
		if got := zone.CivilOffset(january); got != -18000 {
			t.Fatal(year, "January", got)
		}
	}
	before := (civilDay(-4713, 11, 24) - unixEpochCivilDay) * 86400
	if east, _ := zone.OffsetAt(before); east != -17762 {
		t.Fatal("prehistory", east)
	}
	if !zone.goAhead || zone.goBack {
		t.Fatal("expected future repetition but historic default prehistory", zone.goAhead, zone.goBack)
	}
}
func timezoneTZifFixture(chars string, footer string) []byte {
	header := func(n int) []byte {
		b := make([]byte, 44)
		copy(b, "TZif2")
		binary.BigEndian.PutUint32(b[36:40], 1)
		binary.BigEndian.PutUint32(b[40:44], uint32(n))
		return b
	}
	b := append(header(4), make([]byte, 6)...)
	b = append(b, "UTC\x00"...)
	b = append(b, header(len(chars))...)
	b = append(b, make([]byte, 6)...)
	b = append(b, chars...)
	b = append(b, '\n')
	b = append(b, footer...)
	return append(b, '\n')
}
func TestTimezoneTZifFooterCapacityAndRepeat(t *testing.T) {
	accepted, err := parseTZif("fixture", timezoneTZifFixture("UTC\x00", "STD0DST,M3.2.0,M11.1.0"))
	if err != nil || !accepted.goBack || !accepted.goAhead {
		t.Fatal(accepted, err)
	}
	if east, _ := accepted.OffsetAt((civilDay(294276, 7, 1) - unixEpochCivilDay) * 86400); east != 3600 {
		t.Fatal(east)
	}
	rejected, err := parseTZif("fixture", timezoneTZifFixture("UTC\x00"+strings.Repeat("x", 44)+"\x00", "STD0DST,M3.2.0,M11.1.0"))
	if err != nil || rejected.recurrence != nil || rejected.goAhead {
		t.Fatal(rejected, err)
	}
	if east, _ := rejected.OffsetAt((civilDay(294276, 7, 1) - unixEpochCivilDay) * 86400); east != 0 {
		t.Fatal(east)
	}
}

func TestTimezoneProviderSecondsWestDomain(t *testing.T) {
	descriptor := testTypes()[Timetz]
	for _, west := range []int32{-608400, 604800} {
		if _, err := TimetzValue(descriptor, microsPerDay, west); err != nil {
			t.Fatal(west, err)
		}
	}
	for _, west := range []int32{-608401, 604801, 608400} {
		if _, err := TimetzValue(descriptor, 0, west); err == nil {
			t.Fatal("accepted unreachable provider offset", west)
		}
	}
}

func TestTimezoneRebasedCivilCarryAndDateStyle(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	value, err := types[Timestamp].Input(ctx, "4714-11-23 250000 BC", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := types[Timestamp].Output(ctx, value)
	if err != nil || text != "4714-11-24 01:00:00 BC" {
		t.Fatal(text, err)
	}
	date, err := types[Date].Input(ctx, "1997-12-17", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		order DateOrder
		want  string
	}{{DateMDY, "12-17-1997"}, {DateDMY, "17-12-1997"}} {
		text, err := types[Date].OutputWithEnvironment(ctx, date, OutputEnvironment{DateStyle: DateStyle{Style: DatePostgres, Order: tc.order}})
		if err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
	}
}
