package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDatetimeFormatOutputIndependent(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	source, err := TimestampValue(types[Timestamp], 214585922021230)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ template, want string }{
		{"HH HH12 HH24 MI SS SSSS SSSSS", "03 03 15 12 02 54722 54722"},
		{"MS US FF1 FF2 FF3 FF4 FF5 FF6", "021 021230 0 02 021 0212 02123 021230"},
		{"Y,YYY YYYY YYY YY Y IYYY IYY IY I CC Q J", "2,006 2006 006 06 6 2006 006 06 6 21 4 2454028"},
		{"FMMonth Mon MM FMRM FMDay Dy DDD IDDD DD D ID W WW IW", "October Oct 10 X Thursday Thu 292 291 19 5 4 3 42 42"},
		{"AM pm A.M. p.m. AD bc A.D. b.c.", "PM pm P.M. p.m. AD ad A.D. a.d."},
		{"FMMS FMUS FMFF6", "021 021230 021230"},
		{"ddsp DDSP", "19sp 19"},
		{`"Hello Year "YYYY`, "Hello Year 2006"},
	}
	for _, c := range cases {
		plan, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[Timestamp].Usage(), &c.template)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(ctx, source, String(types[Text], c.template), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
		if err != nil {
			t.Fatal(c.template, err)
		}
		got, _ := value.Text()
		if got != c.want {
			t.Errorf("%s: %q want %q", c.template, got, c.want)
		}
	}
	interval, err := IntervalValue(types[Interval], 25, 3, 129732021230)
	if err != nil {
		t.Fatal(err)
	}
	template := "YYYY MM DD HH24:MI:SS.US AM"
	plan, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[Interval].Usage(), &template)
	if err != nil {
		t.Fatal(err)
	}
	value, err := plan.Evaluate(ctx, interval, String(types[Text], template), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := value.Text()
	if got != "0002 01 03 36:02:12.021230 PM" {
		t.Fatal(got)
	}
}

func TestDatetimeFormatInputIndependent(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		source, template string
		days             int64
	}{
		{"05 Dec 2000", "DD Mon YYYY", 339}, {"2006-42-4", "IYYY-IW-ID", 2483}, {"2006-291", "IYYY-IDDD", 2483},
		{"2454028", "J", 2483}, {"2000/JUN", "FXYYYY MON", 152},
	} {
		plan, err := BindDatetimeFormat(ctx, "to_date", types[Date].Usage(), types[Text].Usage(), &c.template)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(ctx, String(types[Text], c.source), String(types[Text], c.template), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
		if err != nil {
			t.Fatal(c.source, err)
		}
		days, _ := value.Date()
		if days != c.days {
			t.Errorf("%s: %d want %d", c.source, days, c.days)
		}
	}
	template := "YYYY-MM-DD HH24:MI:SS.MS.US TZH:TZM"
	plan, err := BindDatetimeFormat(ctx, "to_timestamp", types[Timestamptz].Usage(), types[Text].Usage(), &template)
	if err != nil {
		t.Fatal(err)
	}
	value, err := plan.Evaluate(ctx, String(types[Text], "2006-10-19 15:12:02.020.001230 +05:45"), String(types[Text], template), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := value.Timestamp()
	if n != 214565222021230 {
		t.Fatal("independent offset/fraction instant", n)
	}
}

func TestDatetimeFormatDiagnosticsAndCancellation(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct{ input, template, code, message, detail, hint string }{
		{"2006 2006", "YYYY IYYY", "22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template."},
		{"2006 2007", "YYYY YYYY", "22007", `conflicting values for "YYYY" field in formatting string`, "This value contradicts a previous setting for the same field type.", ""},
		{"20", "YYYYMMDD", "22007", `source string too short for "YYYY" formatting field`, "Field requires 4 characters, but only 2 remain.", datetimeFillHint},
		{"2147483648", "YYYY", "22008", `value for "YYYY" in source string is out of range`, "Value must be in the range -2147483648 to 2147483647.", ""},
		{"2006-02-29", "YYYY-MM-DD", "22008", `date/time field value out of range: "2006-02-29"`, "", ""},
	} {
		plan, err := BindDatetimeFormat(ctx, "to_date", types[Date].Usage(), types[Text].Usage(), &c.template)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plan.Evaluate(ctx, String(types[Text], c.input), String(types[Text], c.template), InputEnvironment{}, OutputEnvironment{})
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: c.code, Message: c.message, Detail: c.detail, Hint: c.hint}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal("complete provider diagnostic", err, want)
		}
		value, err := plan.Evaluate(ctx, Null(types[Text]), String(types[Text], c.template), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !value.IsNull() || value.Type() != types[Date] {
			t.Fatal("typed strict null", value, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := compileDatetime(canceled, strings.Repeat("YYYY", 1024), datetimeSQLFormat); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	template := "YYYY"
	plan, err := BindDatetimeFormat(ctx, "to_date", types[Date].Usage(), types[Text].Usage(), &template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Evaluate(canceled, String(types[Text], "2000"), String(types[Text], template), InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDatetimeFormatConstantTemplateOwnership(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	original, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[Timestamp].Usage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	retained, changed, err := original.WithConstantTemplate(ctx, String(types[Text], "YYYY"))
	if err != nil || !changed {
		t.Fatal("constant template retention", changed, err)
	}
	again, changed, err := retained.WithConstantTemplate(ctx, String(types[Text], "YYYY"))
	if err != nil || changed {
		t.Fatal("retaining an unchanged template", changed, err)
	}
	source, err := TimestampValue(types[Timestamp], 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := retained.Evaluate(ctx, source, String(types[Text], "YYYY"), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := value.Text()
	if got != "2000" {
		t.Fatal(got)
	}
	dynamic, err := original.Evaluate(ctx, source, String(types[Text], "MM"), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	dynamicText, _ := dynamic.Text()
	if dynamicText != "01" {
		t.Fatal("retention mutated the original dynamic plan", dynamicText)
	}
	value, err = again.Evaluate(ctx, source, String(types[Text], "YYYY"), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = value.Text()
	if got != "2000" {
		t.Fatal("retaining the same template changed its result", got)
	}
	replacements := mathTypes()
	rebound, err := retained.Retag(replacements[Text].Usage(), replacements[Timestamp].Usage())
	if err != nil {
		t.Fatal(err)
	}
	source, err = TimestampValue(replacements[Timestamp], 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err = rebound.Evaluate(ctx, source, String(replacements[Text], "YYYY"), InputEnvironment{}, OutputEnvironment{})
	if err != nil || value.Type() != replacements[Text] {
		t.Fatal("constant plan lost retagged authority", value, err)
	}
	if _, err := rebound.Evaluate(ctx, Null(types[Timestamp]), String(replacements[Text], "YYYY"), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("foreign NULL bypassed descriptor authority")
	}
}

func TestDatetimeFormatSyntheticIntervalProjection(t *testing.T) {
	for _, c := range []struct {
		year, month, day int
		julian           int32
	}{
		{2000, 1, 1, 2451545}, {0, 0, 0, 1721028}, {-1, -1, -1, 1720632},
	} {
		if got := datetimeIntervalJulian(c.year, c.month, c.day); got != c.julian {
			t.Fatal("signed synthetic Julian projection", c, got)
		}
	}
	for _, c := range []struct{ year, month, day, isoYear, week, yearday int }{
		{0, 0, 0, -1, 48, 331}, {-1, -1, -1, -2, 44, 306},
	} {
		year, week, day := datetimeIntervalISO(c.year, c.month, c.day)
		if year != c.isoYear || week != c.week || day != c.yearday {
			t.Fatal("synthetic ISO projection", c, year, week, day)
		}
	}
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct {
		months, days   int32
		template, want string
	}{
		{2147483647, 2147483647, "DDD", "2147483617"},
		{0, -2147483648, "DDD WW W", "-2147483648 306783379 306783379"},
		{-13, -1, "J IYYY IW IDDD WW W", "1720632 -0002 44 306 -55 1"},
	} {
		source, err := IntervalValue(types[Interval], c.months, c.days, 0)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[Interval].Usage(), &c.template)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(ctx, source, String(types[Text], c.template), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := value.Text()
		if got != c.want {
			t.Fatal("fixed-width synthetic fields", c, got)
		}
	}
}

func TestDatetimeFormatWhitespaceDemand(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"to_date", "to_timestamp"} {
		result := types[Date]
		if name == "to_timestamp" {
			result = types[Timestamptz]
		}
		for _, c := range []struct {
			input, mask, message, detail, hint string
			days, instant                      int64
		}{
			{input: "   ", mask: "YYYY", message: `invalid value "" for "YYYY"`, detail: "Value must be an integer."},
			{input: "\t\r\n", mask: "YYYY", message: `invalid value "" for "YYYY"`, detail: "Value must be an integer."},
			{input: "   ", mask: "MON", message: `invalid value "" for "MON"`, detail: "The given value did not match any of the allowed values for this field."},
			{input: "   ", mask: "DY", message: `invalid value "" for "DY"`, detail: "The given value did not match any of the allowed values for this field."},
			{input: "   ", mask: "RM", message: `invalid value "" for "RM"`, detail: "The given value did not match any of the allowed values for this field."},
			{input: "   ", mask: "YYYYMMDD", message: `source string too short for "YYYY" formatting field`, detail: "Field requires 4 characters, but only 0 remain.", hint: datetimeFillHint},
			{input: "   ", mask: "FXYYYY", message: `invalid value "" for "YYYY"`, detail: "Value must be an integer."},
			{input: "   ", mask: "FXMON", message: `invalid value "" for "MON"`, detail: "The given value did not match any of the allowed values for this field."},
			{input: "2000   ", mask: "FXYYYY MON", message: `invalid value "" for "MON"`, detail: "The given value did not match any of the allowed values for this field."},
			{input: "", mask: "YYYY", days: -730485, instant: -63113904000000000},
			{input: "", mask: "FXYYYYMMDD", days: -730485, instant: -63113904000000000},
			{input: "   ", mask: `"abc"YYYY`, days: -730485, instant: -63113904000000000},
			{input: "   ", mask: "/YYYY", days: -730485, instant: -63113904000000000},
			{input: "2000", mask: "YYYY MM"},
			{input: "2000   ", mask: "YYYY FXMON"},
		} {
			for _, constant := range []bool{false, true} {
				var template *string
				if constant {
					template = &c.mask
				}
				plan, err := BindDatetimeFormat(ctx, name, result.Usage(), types[Text].Usage(), template)
				if err != nil {
					t.Fatal(err)
				}
				value, err := plan.Evaluate(ctx, String(types[Text], c.input), String(types[Text], c.mask), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
				if c.message != "" {
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: c.message, Detail: c.detail, Hint: c.hint}
					var got *sqlstate.Error
					if !errors.As(err, &got) || *got != want {
						t.Fatalf("%s %q/%q constant=%t: %v want %#v", name, c.input, c.mask, constant, err, want)
					}
					continue
				}
				if err != nil || value.Type() != result {
					t.Fatalf("%s %q/%q: value=%v error=%v", name, c.input, c.mask, value, err)
				}
				if name == "to_date" {
					days, valueErr := value.Date()
					if valueErr != nil || days != c.days {
						t.Fatalf("%q/%q days=%d want %d", c.input, c.mask, days, c.days)
					}
				} else {
					instant, valueErr := value.Timestamp()
					if valueErr != nil || instant != c.instant {
						t.Fatalf("%q/%q instant=%d want %d", c.input, c.mask, instant, c.instant)
					}
				}
			}
		}
	}
}

func TestDatetimeFormatLocalizedInputDemand(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"to_date", "to_timestamp"} {
		result := types[Date]
		if name == "to_timestamp" {
			result = types[Timestamptz]
		}
		for _, c := range []struct {
			input, mask, message, detail, hint, code string
			days, instant                            int64
		}{
			{input: "2000", mask: "YYYY TMMon"},
			{input: "05 Dec 2000", mask: "DD TMMon YYYY", days: 339, instant: 29289600000000},
			{input: "2006-10-19 Thursday", mask: "YYYY-MM-DD TMDay", days: 2483, instant: 214531200000000},
			{input: "05 dEc 2000", mask: "DD TMMon YYYY", code: "42P22", message: "could not determine which collation to use for upper() function", hint: "Use the COLLATE clause to set the collation explicitly."},
			{input: "05 Bogus 2000", mask: "DD TMMon YYYY", code: "42P22", message: "could not determine which collation to use for upper() function", hint: "Use the COLLATE clause to set the collation explicitly."},
			{input: "bad Dec 2000", mask: "DD TMMon YYYY", code: "22007", message: `invalid value "ba" for "DD"`, detail: "Value must be an integer."},
			{input: "2000 2000 Dec", mask: "YYYY IYYY TMMon", code: "22007", message: "invalid combination of date conventions", hint: "Do not mix Gregorian and ISO week date conventions in a formatting template."},
			{input: "", mask: "TMMonth", days: -730485, instant: -63113904000000000},
			{input: "05 dEc 2000", mask: "DD Mon YYYY", days: 339, instant: 29289600000000},
		} {
			for _, constant := range []bool{false, true} {
				var template *string
				if constant {
					template = &c.mask
				}
				plan, err := BindDatetimeFormat(ctx, name, result.Usage(), types[Text].Usage(), template)
				if err != nil {
					t.Fatal(err)
				}
				plan = plan.WithCollationConflict(true)
				value, err := plan.Evaluate(ctx, String(types[Text], c.input), String(types[Text], c.mask), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
				if c.code != "" {
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: c.code, Message: c.message, Detail: c.detail, Hint: c.hint}
					var got *sqlstate.Error
					if !errors.As(err, &got) || *got != want {
						t.Fatalf("%s %q/%q: %v want %#v", name, c.input, c.mask, err, want)
					}
				} else {
					if err != nil || value.Type() != result {
						t.Fatalf("%s %q/%q: %v", name, c.input, c.mask, err)
					}
					if name == "to_date" {
						days, valueErr := value.Date()
						if valueErr != nil || days != c.days {
							t.Fatalf("days=%d want %d", days, c.days)
						}
					} else {
						instant, valueErr := value.Timestamp()
						if valueErr != nil || instant != c.instant {
							t.Fatalf("instant=%d want %d", instant, c.instant)
						}
					}
				}
				value, err = plan.Evaluate(ctx, Null(types[Text]), String(types[Text], c.mask), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
				if err != nil || !value.IsNull() || value.Type() != result {
					t.Fatal("strict NULL demanded localized conversion", value, err)
				}
			}
		}
	}
}

func TestDatetimeFormatLocalizedOutputDemand(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceID := range []ID{Timestamp, Timestamptz, Interval} {
		var finite, positive, negative Value
		if sourceID == Interval {
			finite, err = IntervalValue(types[Interval], 0, 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			positive, err = types[Interval].InputWithEnvironment(ctx, "infinity", -1, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			negative, err = types[Interval].InputWithEnvironment(ctx, "-infinity", -1, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			finite, err = TimestampValue(types[sourceID], 0)
			if err != nil {
				t.Fatal(err)
			}
			positive, err = TimestampValue(types[sourceID], TimestampPositiveInfinity)
			if err != nil {
				t.Fatal(err)
			}
			negative, err = TimestampValue(types[sourceID], TimestampNegativeInfinity)
			if err != nil {
				t.Fatal(err)
			}
		}
		for i, mask := range []string{"TMMonth", "TMMONTH", "TMmonth", "DAY TMMonth"} {
			plan, err := BindDatetimeFormat(ctx, "to_char", types[Text].Usage(), types[sourceID].Usage(), &mask)
			if err != nil {
				t.Fatal(err)
			}
			plan = plan.WithCollationConflict(true)
			for _, value := range []Value{Null(types[sourceID]), positive, negative} {
				got, err := plan.Evaluate(ctx, value, String(types[Text], mask), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
				if err != nil || !got.IsNull() || got.Type() != types[Text] {
					t.Fatal("infinity/NULL demanded conversion", sourceID, mask, got, err)
				}
			}
			_, err = plan.Evaluate(ctx, finite, String(types[Text], mask), InputEnvironment{Zone: utc}, OutputEnvironment{Zone: utc})
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for " + []string{"initcap()", "upper()", "lower()", "initcap()"}[i] + " function", Hint: "Use the COLLATE clause to set the collation explicitly."}
			if sourceID == Interval {
				want.Code = "22007"
				want.Message = "invalid format specification for an interval value"
				want.Hint = "Intervals are not tied to specific calendar dates."
			}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("%v %q: %v want %#v", sourceID, mask, err, want)
			}
		}
	}
}
