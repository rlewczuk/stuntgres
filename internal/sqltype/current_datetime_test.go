package sqltype

import (
	"context"
	"testing"
	"time"
)

func TestCurrentDateTimeKindsAndPrecision(t *testing.T) {
	types := testTypes()
	clock := time.Date(2000, 1, 1, 23, 59, 59, 999999000, time.UTC)
	for _, tc := range []struct {
		id        ID
		precision int32
		want      string
	}{
		{Date, -1, "2000-01-01"}, {Time, 0, "24:00:00"}, {Timetz, 0, "24:00:00+00"},
		{Timestamp, 0, "2000-01-02 00:00:00"}, {Timestamptz, 0, "2000-01-02 00:00:00+00"},
		{Time, 6, "23:59:59.999999"}, {Timetz, -1, "23:59:59.999999+00"},
	} {
		usage := types[tc.id].Usage()
		usage.Typmod = tc.precision
		value, err := CurrentDateTime(usage, InputEnvironment{TransactionStart: clock})
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := types[tc.id].Output(context.Background(), value)
		if err != nil || got != tc.want {
			t.Fatalf("%v got=%q err=%v want=%q", tc, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		fraction  int
		precision int32
		want      int64
	}{
		{123499000, 3, 123000}, {123500000, 3, 124000}, {123501000, 3, 124000},
		{500000000, 0, 1000000}, {499999000, 0, 0},
	} {
		for _, id := range []ID{Time, Timetz, Timestamp, Timestamptz} {
			usage := types[id].Usage()
			usage.Typmod = tc.precision
			value, err := CurrentDateTime(usage, InputEnvironment{TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, tc.fraction, time.UTC)})
			if err != nil || value.integer != tc.want {
				t.Fatal(id, tc, value, err)
			}
		}
	}
	// Signed timestamp epoch rounding must remain half-away-from-zero.
	for _, id := range []ID{Timestamp, Timestamptz} {
		usage := types[id].Usage()
		usage.Typmod = 0
		value, err := CurrentDateTime(usage, InputEnvironment{TransactionStart: time.Date(1999, 12, 31, 23, 59, 59, 500000000, time.UTC)})
		if err != nil || value.integer != -1000000 {
			t.Fatal(id, value, err)
		}
	}
	for _, tc := range []struct {
		zone  string
		clock time.Time
		want  [5]string
	}{
		{"Pacific/Kiritimati", time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC), [5]string{"2000-01-02", "02:00:00+14", "2000-01-02 02:00:00+14", "02:00:00", "2000-01-02 02:00:00"}},
		{"Etc/GMT+12", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), [5]string{"1999-12-31", "12:00:00-12", "1999-12-31 12:00:00-12", "12:00:00", "1999-12-31 12:00:00"}},
		{"America/New_York", time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC), [5]string{"2020-01-01", "07:00:00-05", "2020-01-01 07:00:00-05", "07:00:00", "2020-01-01 07:00:00"}},
		{"America/New_York", time.Date(2020, 7, 1, 12, 0, 0, 0, time.UTC), [5]string{"2020-07-01", "08:00:00-04", "2020-07-01 08:00:00-04", "08:00:00", "2020-07-01 08:00:00"}},
		// The rounded instant crosses the DST boundary, but civil producers and
		// timetz offset are derived before rounding at the unrounded instant.
		{"America/New_York", time.Date(2020, 3, 8, 6, 59, 59, 999999000, time.UTC), [5]string{"2020-03-08", "02:00:00-05", "2020-03-08 03:00:00-04", "02:00:00", "2020-03-08 02:00:00"}},
	} {
		env := timezoneEnvironment(t, tc.zone, tc.clock)
		for i, id := range []ID{Date, Timetz, Timestamptz, Time, Timestamp} {
			usage := types[id].Usage()
			if id != Date {
				usage.Typmod = 0
			}
			value, err := CurrentDateTime(usage, env)
			if err != nil {
				t.Fatal(tc, id, err)
			}
			got, err := types[id].OutputWithEnvironment(context.Background(), value, OutputEnvironment{Zone: env.Zone})
			if err != nil || got != tc.want[i] {
				t.Fatalf("zone=%s type=%d got=%q err=%v want=%q", tc.zone, id, got, err, tc.want[i])
			}
		}
	}
}

func TestCurrentDateTimeClockQuantization(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		ns int
		us int64
	}{{999, 0}, {1000, 1}, {1500, 1}, {1999, 1}} {
		for _, id := range []ID{Time, Timetz, Timestamp, Timestamptz} {
			usage := types[id].Usage()
			for _, precision := range []int32{-1, 6} {
				usage.Typmod = precision
				value, err := CurrentDateTime(usage, InputEnvironment{TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, tc.ns, time.UTC)})
				if err != nil || value.integer != tc.us {
					t.Fatal(tc, id, precision, value, err)
				}
			}
		}
	}
	for _, env := range []InputEnvironment{{}, {TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		usage := Usage{}
		if env.TransactionStart.IsZero() {
			usage = types[Time].Usage()
		}
		if _, err := CurrentDateTime(usage, env); err == nil {
			t.Fatal("invalid producer context accepted", usage, env)
		}
	}
	if _, err := CurrentDateTime(types[Int4].Usage(), InputEnvironment{TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Fatal("non-temporal producer accepted")
	}
	usage := types[Timestamp].Usage()
	env := InputEnvironment{TransactionStart: time.Date(2000, 1, 1, 0, 0, 0, 123456000, time.UTC)}
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := CurrentDateTime(usage, env); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("successful producer allocations", allocs)
	}
}
