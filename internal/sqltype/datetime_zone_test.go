package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestDatetimeZoneCalendarExplicitAndAllocation(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	input := timezoneEnvironment(t, "UTC", time.Date(2020, 7, 1, 12, 0, 0, 0, time.UTC))
	day, err := types[Interval].Input(ctx, "1 day", -1)
	if err != nil {
		t.Fatal(err)
	}
	add, err := BindArithmetic(ArithmeticAdd, types[Timestamptz], types[Interval], types[Timestamptz])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ zone, source, want string }{
		{"America/New_York", "2018-03-10 17:00+00", "2018-03-11 16:00:00+00"},
		{"EST", "2018-03-10 17:00+00", "2018-03-11 17:00:00+00"},
		{"MSK", "2010-03-27 09:00+00", "2010-03-28 08:00:00+00"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			stamp, err := types[Timestamptz].Input(ctx, tc.source, -1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := CalendarArithmetic(ctx, add, stamp, day, tc.zone, input)
			if err != nil {
				t.Fatal(err)
			}
			text, err := got.Type().Output(ctx, got)
			if err != nil || text != tc.want {
				t.Fatal(text, err, tc.want)
			}
			// The preceding call warms only the existing shared immutable registry.
			if allocations := testing.AllocsPerRun(100, func() {
				_, failure := CalendarArithmetic(ctx, add, stamp, day, tc.zone, input)
				if failure != nil {
					panic(failure)
				}
			}); allocations != 0 {
				t.Fatalf("warmed calendar provider allocates %g times", allocations)
			}
		})
	}
	inf, err := types[Timestamptz].Input(ctx, "infinity", -1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CalendarArithmetic(ctx, add, inf, day, "No/Such_Zone", input)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `time zone "No/Such_Zone" not recognized` {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CalendarArithmetic(canceled, add, inf, day, "UTC", input); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDatetimeZoneIntervalReleasedDisplacements(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	input := timezoneEnvironment(t, "UTC", time.Time{})
	for _, tc := range []struct{ seconds, stamp, instant, clock string }{
		{"2147483647", "1935-12-14 08:45:53+00", "2072-01-19 15:14:07", "15:14:07+596523:14:07"},
		{"2147483648", "2072-01-19 15:14:08+00", "2072-01-19 15:14:08", "08:45:52+4294370773:4294967282:4294967288"},
		{"4294967296", "2004-01-01 12:00:00+00", "2004-01-01 12:00:00", "12:00:00+00"},
	} {
		offset, err := types[Interval].Input(ctx, tc.seconds+" seconds", -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []struct {
			from, to    ID
			value, want string
		}{
			{Timestamp, Timestamptz, "2004-01-01 12:00", tc.stamp},
			{Timestamptz, Timestamp, "2004-01-01 12:00+00", tc.instant},
			{Timetz, Timetz, "12:00+00", tc.clock},
		} {
			value, err := types[source.from].Input(ctx, source.value, -1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AtTimeZoneInterval(value, types[source.to], offset, input)
			if err != nil {
				t.Fatal(tc, source, err)
			}
			text, err := got.Type().Output(ctx, got)
			if err != nil || text != source.want {
				t.Fatal(tc, source, text, err)
			}
			if source.to == Timetz {
				west, err := got.TimeZoneOffset()
				if err != nil {
					t.Fatal(err)
				}
				if west < minProviderSecondsWest || west > maxProviderSecondsWest {
					if _, err := TimetzValue(types[Timetz], got.integer, west); err == nil {
						t.Fatal("constructor input domain widened")
					}
					literal, err := got.Type().SQLLiteral(ctx, got)
					if err != nil || literal == "" {
						t.Fatal(literal, err)
					}
					zero, err := types[Interval].Input(ctx, "0 seconds", -1)
					if err != nil {
						t.Fatal(err)
					}
					arithmetic, err := BindArithmetic(ArithmeticAdd, types[Timetz], types[Interval], types[Timetz])
					if err != nil {
						t.Fatal(err)
					}
					retained, err := arithmetic.Binary(ctx, got, zero, input)
					if err != nil || !SameDatum(got, retained) {
						t.Fatal("converted displacement lost", retained, err)
					}
				}
			}
		}
	}
	for _, id := range []ID{Timestamp, Timestamptz} {
		value, err := types[id].Input(ctx, "infinity", -1)
		if err != nil {
			t.Fatal(err)
		}
		target := types[Timestamp]
		if id == Timestamp {
			target = types[Timestamptz]
		}
		if _, err := AtTimeZone(value, target, "No/Such_Zone", false, input); err != nil {
			t.Fatal(err)
		}
		month, err := types[Interval].Input(ctx, "1 month", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AtTimeZoneInterval(value, target, month, input); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDatetimeZoneCalendarIgnoresSessionAbbreviationOverride(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	stamp, err := types[Timestamptz].Input(ctx, "2024-01-30 20:00+00", -1)
	if err != nil {
		t.Fatal(err)
	}
	month, err := types[Interval].Input(ctx, "1 month", -1)
	if err != nil {
		t.Fatal(err)
	}
	add, err := BindArithmetic(ArithmeticAdd, types[Timestamptz], types[Interval], types[Timestamptz])
	if err != nil {
		t.Fatal(err)
	}
	// Both incoming override routes would select EST +10 and cross midnight.
	// Selected explicit calendar lookup must instead use stock Default EST -5.
	input := InputEnvironment{Zone: FixedZone("EST", 10*3600), TimezoneAbbreviations: TimezoneAbbreviationsAustralia}
	value, err := CalendarArithmetic(ctx, add, stamp, month, "EST", input)
	if err != nil {
		t.Fatal(err)
	}
	text, err := types[Timestamptz].Output(ctx, value)
	if err != nil || text != "2024-02-29 20:00:00+00" {
		t.Fatal(text, err)
	}
}
