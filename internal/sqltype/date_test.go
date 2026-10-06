package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func dateDescriptor(t *testing.T) *Descriptor {
	t.Helper()
	for _, d := range Builtins() {
		if d.ID() == Date {
			return d
		}
	}
	t.Fatal("missing DATE OID1082")
	return nil
}

func TestDateIndependentDayConstants(t *testing.T) {
	d := dateDescriptor(t)
	ctx := context.Background()
	for _, tc := range []struct {
		input string
		days  int64
	}{
		{"2000-01-01", 0}, {"1970-01-01", -10957}, {"2000-02-29", 59},
		{"4714-11-24 BC", -2451545}, {"5874897-12-31", 2145031948},
		{"-infinity", DateNegativeInfinity}, {"infinity", DatePositiveInfinity},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		got, err := v.Date()
		if err != nil || got != tc.days {
			t.Fatal(tc.input, got, err, tc.days)
		}
		v, err = DateValue(d, tc.days)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.input {
			t.Fatal(out, err, tc.input)
		}
	}
	for _, days := range []int64{-2451546, 2145031949} {
		if _, err := DateValue(d, days); err == nil {
			t.Fatal("outside finite domain accepted", days)
		}
	}
	if _, err := Null(d).Date(); err == nil {
		t.Fatal("NULL exported as a day")
	}
}

func TestDateRelativeEnvironmentAndTimeCivilFields(t *testing.T) {
	d := dateDescriptor(t)
	ctx := context.Background()
	for _, tc := range []struct {
		start  time.Time
		values []string
	}{
		{time.Date(2000, 3, 1, 23, 59, 59, 999999999, time.UTC), []string{"2000-03-01", "2000-03-01", "2000-02-29", "2000-03-02"}},
		{time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC), []string{"1999-12-31", "1999-12-31", "1999-12-30", "2000-01-01"}},
	} {
		for i, input := range []string{"now", "today", "yesterday", "tomorrow"} {
			v, err := d.InputWithEnvironment(ctx, input, -1, InputEnvironment{TransactionStart: tc.start})
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.values[i] {
				t.Fatal(input, out, err, tc.values[i])
			}
		}
	}
	for _, tc := range []struct {
		value time.Time
		want  string
	}{
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 14*3600)), "2000-01-01"},
		{time.Date(1999, 12, 31, 23, 59, 59, 0, time.FixedZone("west", -12*3600)), "1999-12-31"},
		{time.Date(0, 2, 29, 23, 59, 59, 0, time.UTC), "0001-02-29 BC"},
	} {
		v, err := DateFromTime(d, tc.value)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatal(out, err, tc.want)
		}
	}
	for _, tc := range []struct {
		input   time.Time
		message string
	}{
		{time.Date(-4714, 1, 1, 0, 0, 0, 0, time.UTC), `date out of range: "4715-01-01 00:00:00Z BC"`},
		{time.Date(5874898, 1, 1, 0, 0, 0, 0, time.UTC), `date out of range: "5874898-01-01 00:00:00Z"`},
		{time.Date(-4713, 11, 23, 0, 0, 0, 0, time.UTC), `date out of range: "4714-11-23 00:00:00Z BC"`},
		{time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.UTC), `date/time field value out of range: "2147483648-01-01 00:00:00Z"`},
		{time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.UTC), `date/time field value out of range: "2147483648-01-01 00:00:00Z BC"`},
		{time.Date(5874898, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384)), `date out of range: "5874898-01-01 04:05:06.123456789+02:03:04"`},
		{time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384)), `date out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`},
	} {
		_, err := DateFromTime(d, tc.input)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: tc.message}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	v, err := DateFromTime(d, time.Date(5874897, 12, 31, 23, 59, 59, 999999999, time.FixedZone("east", 14*3600)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Output(ctx, v)
	if err != nil || out != "5874897-12-31" {
		t.Fatal(out, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(ctx, "2000-01-01", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDateRepeatedSeparatorIsCivilInput(t *testing.T) {
	d := dateDescriptor(t)
	v, err := d.Input(context.Background(), "2001--01-01", -1)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Output(context.Background(), v)
	if err != nil || out != "2001-01-01" {
		t.Fatal(out, err)
	}
}

func TestDateFromTimeAllocationFreeCivilImport(t *testing.T) {
	d := dateDescriptor(t)
	for _, tc := range []struct {
		input time.Time
		days  int64
	}{
		{time.Date(2000, 1, 1, 23, 59, 59, 999999999, time.FixedZone("east", 16*3600)), 0},
		{time.Date(-4713, 11, 24, 23, 59, 59, 999999999, time.FixedZone("west", -16*3600)), -2451545},
		{time.Date(5874897, 12, 31, 23, 59, 59, 999999999, time.FixedZone("east", 7384)), 2145031948},
	} {
		var value Value
		var err error
		allocations := testing.AllocsPerRun(1000, func() { value, err = DateFromTime(d, tc.input) })
		if err != nil {
			t.Fatal(err)
		}
		days, err := value.Date()
		if err != nil || days != tc.days || allocations != 0 {
			t.Fatal(days, err, allocations, tc.days)
		}
	}
}

func TestDateSharedTemporalZoneAndCoreTokens(t *testing.T) {
	d := dateDescriptor(t)
	ctx := context.Background()
	env := InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)}
	for _, tc := range []struct{ input, want string }{
		{"1999-01-08 04:05:06.1e2", "1999-01-08"},
		{"1999-01-08 04:05 X0", "1999-01-08"},
		{"1999-01-08 04:05 X0Y0", "1999-01-08"},
		{"1999-01-08 04:05 X0Y", "1999-01-08"},
		{"today+2", "2024-12-31"},
	} {
		v, err := d.InputWithEnvironment(ctx, tc.input, -1, env)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatal(tc.input, out, err, tc.want)
		}
	}
	for _, input := range []string{"1999-01-08 04:05:06 now1", "1999-01-08 04:05:06 now+2"} {
		_, err := d.InputWithEnvironment(ctx, input, -1, env)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type date: "` + input + `"`}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
}
