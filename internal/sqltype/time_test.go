package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func timeDescriptor(t *testing.T) *Descriptor {
	t.Helper()
	for _, d := range Builtins() {
		if d.ID() == Time {
			return d
		}
	}
	t.Fatal("missing OID1083")
	return nil
}

func TestTimeCodecInputs(t *testing.T) {
	d := timeDescriptor(t)
	ctx := context.Background()
	env := InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 15, 36, 39, 123456000, time.UTC)}
	for _, tc := range []struct{ input, want string }{
		{"00:00", "00:00:00"}, {"24:00", "24:00:00"}, {"04:05:06.789", "04:05:06.789"},
		{"04:05", "04:05:00"}, {"040506", "04:05:06"}, {"0405", "04:05:00"}, {"04:05.5", "00:04:05.5"}, {"4:5:6", "04:05:06"},
		{"04:05 AM", "04:05:00"}, {"04:05 pm", "16:05:00"}, {"12:00 AM", "00:00:00"}, {"12:00 PM", "12:00:00"}, {"00:00 AM", "00:00:00"}, {"00:00 PM", "12:00:00"},
		{"04:05:06.789-8", "04:05:06.789"}, {"04:05:06-08:00", "04:05:06"}, {"04:05:06+0730", "04:05:06"}, {"04:05:06+07:30:00", "04:05:06"},
		{"02:03 PST", "02:03:00"}, {"11:59 EDT", "11:59:00"}, {"04:05 MET DST", "04:05:00"}, {"04:05 MSK", "04:05:00"},
		{"2003-03-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39 America/New_York", "15:36:39"},
		{"2003-07-07 15:36:39", "15:36:39"}, {"2003-07-07 15:36:39 Asia/Kolkata", "15:36:39"},
		{"04:05 Etc/GMT+5", "04:05:00"}, {"04:05 eTc/UtC", "04:05:00"}, {"04:05 Universal", "04:05:00"},
		{"04:05 X0", "04:05:00"}, {"04:05 X0Y0", "04:05:00"}, {"2003-07-07 04:05 X0Y", "04:05:00"},
		{"t040506", "04:05:06"}, {"t04:05:06", "04:05:06"}, {"j2451545.5", "12:00:00"}, {"j2451545 04:05", "04:05:00"},
		{"04:05 on", "04:05:00"}, {"04:05 AD", "04:05:00"}, {"allballs", "00:00:00"}, {"now", "15:36:39.123456"}, {"now UTC", "15:36:39.123456"},
		{"23:59:60", "24:00:00"}, {"23:59:59.9999999", "24:00:00"}, {"04:05:60", "04:06:00"}, {"04:05:06.0000005", "04:05:06"}, {"04:05:06.0000015", "04:05:06.000002"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.InputWithEnvironment(ctx, tc.input, -1, env)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.want {
				t.Fatalf("got %q %v want %q", out, err, tc.want)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+tc.want+"'" {
				t.Fatal(literal, err)
			}
		})
	}
}

func TestTimeCodecFailures(t *testing.T) {
	d := timeDescriptor(t)
	for _, tc := range []struct{ input, code, message, hint string }{
		{"25:00", "22008", "date/time field value out of range", ""}, {"12:60", "22008", "date/time field value out of range", ""},
		{"12:00:61", "22008", "date/time field value out of range", ""}, {"24:01", "22008", "date/time field value out of range", ""},
		{"24:00:00.1", "22008", "date/time field value out of range", ""}, {"23:59:60.01", "22008", "date/time field value out of range", ""},
		{"250000", "22008", "date/time field value out of range", ""}, {"049900", "22008", "date/time field value out of range", ""}, {"040599", "22008", "date/time field value out of range", ""},
		{"13:00 PM", "22008", "date/time field value out of range", ""}, {"04:05+16", "22009", "time zone displacement out of range", ""},
		{"2003-02-29 04:05", "22008", "date/time field value out of range", ""},
		{"2003-13-07 04:05", "22008", "date/time field value out of range", "Perhaps you need a different \"DateStyle\" setting."},
		{"bad", "22007", "invalid input syntax for type time", ""}, {"epoch", "22007", "invalid input syntax for type time", ""},
		{"infinity", "22007", "invalid input syntax for type time", ""}, {"today", "22007", "invalid input syntax for type time", ""},
		{"04:05 on Monday", "22007", "invalid input syntax for type time", ""}, {"Monday 04:05", "22007", "invalid input syntax for type time", ""},
		{"2003-07-07 04:05 Monday", "22007", "invalid input syntax for type time", ""},
		{"2003-07-07", "22007", "invalid input syntax for type time", ""}, {"20030707 0405", "22007", "invalid input syntax for type time", ""},
		{"04:05 America/New_York", "22007", "invalid input syntax for type time", ""}, {"04:05 Asia/Kolkata", "22007", "invalid input syntax for type time", ""},
		{"04:05 X0Y", "22007", "invalid input syntax for type time", ""}, {"04:05 AM PM", "22007", "invalid input syntax for type time", ""},
		{"04:05 UTC UTC", "22007", "invalid input syntax for type time", ""}, {"04:05 EDT DST", "22007", "invalid input syntax for type time", ""},
		{"04:05 MSK DST", "22007", "invalid input syntax for type time", ""}, {"04:05 Etc/UTC DST", "22007", "invalid input syntax for type time", ""},
		{"04:05 DST", "22007", "invalid input syntax for type time", ""}, {"allballs UTC", "22007", "invalid input syntax for type time", ""},
		{"04:05:06 040506", "22007", "invalid input syntax for type time", ""}, {"t", "22007", "invalid input syntax for type time", ""}, {"y2003 04:05", "22007", "invalid input syntax for type time", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			_, err := d.Input(context.Background(), tc.input, 0)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message + ": \"" + tc.input + "\"", Hint: tc.hint}
			if !reflect.DeepEqual(err, want) {
				t.Fatalf("got %#v want %#v", err, want)
			}
		})
	}
	_, err := d.Input(context.Background(), "04:05 America/Bogus", -1)
	if !reflect.DeepEqual(err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "time zone \"america/bogus\" not recognized"}) {
		t.Fatal(err)
	}
}

// REL_18_0 DecodeTimeOnly processes token masks, then ValidateDate, AM/PM,
// and finally time_overflows. These expectations are independent of the codec.
func TestTimeClockErrorPrecedence(t *testing.T) {
	d := timeDescriptor(t)
	for _, tc := range []struct{ input, code, message, hint string }{
		{"25:00 UTC UTC", "22007", "invalid input syntax for type time: \"25:00 UTC UTC\"", ""},
		{"UTC UTC 25:00", "22007", "invalid input syntax for type time: \"UTC UTC 25:00\"", ""},
		{"24:00:00.1 America/Bogus", "22023", "time zone \"america/bogus\" not recognized", ""},
		{"America/Bogus 24:00:00.1", "22007", "invalid input syntax for type time: \"America/Bogus 24:00:00.1\"", ""},
		{"2003-13-07 25:00", "22008", "date/time field value out of range: \"2003-13-07 25:00\"", "Perhaps you need a different \"DateStyle\" setting."},
		{"25:00 2003-13-07", "22007", "invalid input syntax for type time: \"25:00 2003-13-07\"", ""},
		{"25:00 AM PM", "22007", "invalid input syntax for type time: \"25:00 AM PM\"", ""},
		{"AM PM 25:00", "22007", "invalid input syntax for type time: \"AM PM 25:00\"", ""},
		{"12:60 America/Bogus", "22008", "date/time field value out of range: \"12:60 America/Bogus\"", ""},
		{"America/Bogus 12:60", "22007", "invalid input syntax for type time: \"America/Bogus 12:60\"", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			_, err := d.Input(context.Background(), tc.input, -1)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
			if !reflect.DeepEqual(err, want) {
				t.Fatalf("got %#v want %#v", err, want)
			}
		})
	}
}

func TestTimePrecisionAndComparison(t *testing.T) {
	d := timeDescriptor(t)
	ctx := context.Background()
	if d.Name() != "time" || d.ID() != 1083 || d.DatabaseName() != "TIME" || d.DiagnosticName() != "time without time zone" {
		t.Fatal(d)
	}
	for p := int32(0); p <= 6; p++ {
		u, w, err := d.ResolveModifiers(p)
		if err != nil || w != nil || u.Typmod != p || !u.Valid() {
			t.Fatal(u, w, err)
		}
		v, err := d.Input(ctx, "04:05:06.123456", p)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := v.Time()
		unit := int64(1)
		for i := p; i < 6; i++ {
			unit *= 10
		}
		want := (int64(14706123456) + unit/2) / unit * unit
		if n != want {
			t.Fatal(p, n, want)
		}
	}
	for _, tc := range []struct {
		p           int32
		input, want string
	}{{0, "04:05:06.499999", "04:05:06"}, {0, "04:05:06.5", "04:05:07"}, {3, "04:05:06.1234999", "04:05:06.124"}, {0, "23:59:59.5", "24:00:00"}} {
		v, err := d.Input(ctx, tc.input, tc.p)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := d.Output(ctx, v)
		if out != tc.want {
			t.Fatal(tc, out)
		}
	}
	u, w, err := d.ResolveModifiers(7)
	if err != nil || u.Typmod != 6 || w == nil || w.Code != "22023" || w.Message != "TIME(7) precision reduced to maximum allowed, 6" {
		t.Fatal(u, w, err)
	}
	_, _, err = d.ResolveModifiers(-1)
	if !reflect.DeepEqual(err, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "TIME(-1) precision must not be negative"}) {
		t.Fatal(err)
	}
	cmp, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int64{{0, 14706100000}, {14706100000, 14706200000}, {14706200000, microsPerDay}, {0, microsPerDay}} {
		a, _ := TimeValue(d, pair[0])
		b, _ := TimeValue(d, pair[1])
		n, _ := cmp.Compare(ctx, a, b)
		reverse, _ := cmp.Compare(ctx, b, a)
		equal, _ := cmp.Compare(ctx, a, a)
		if n != -1 || reverse != 1 || equal != 0 {
			t.Fatal(pair, n, reverse, equal)
		}
	}
	var boolType, stamp *Descriptor
	for _, v := range Builtins() {
		if v.ID() == Bool {
			boolType = v
		}
		if v.ID() == Timestamp {
			stamp = v
		}
	}
	eq, _ := BindComparisonPredicate(CompareEqual, d, d, boolType)
	v, _ := TimeValue(d, 0)
	null, err := eq.Evaluate(ctx, Null(d), v)
	if err != nil || !null.IsNull() || null.Type() != boolType {
		t.Fatal(null, err)
	}
	if _, err = BindComparison(d, stamp); err == nil {
		t.Fatal("cross temporal comparison admitted")
	}
	if _, err = TimeValue(d, -1); err == nil {
		t.Fatal("negative clock")
	}
	if _, err = TimeValue(d, microsPerDay+1); err == nil {
		t.Fatal("overflow clock")
	}
	if _, err = Null(d).Time(); err == nil {
		t.Fatal("NULL accessor")
	}
	if _, err = v.Timestamp(); err == nil {
		t.Fatal("foreign accessor")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = d.Input(ctx, "00:00", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
