package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONPathDatetimeTypedValues(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		doc, path, text string
		id              ID
		integer         int64
		west            int32
	}{
		{`"2023-08-15"`, `$.datetime()`, "2023-08-15", Date, 8627, 0},
		{`"12:34:56.789"`, `$.datetime()`, "12:34:56.789", Time, 45296789000, 0},
		{`"12:34:56.789+05:30"`, `$.datetime()`, "12:34:56.789+05:30", Timetz, 45296789000, -19800},
		{`"2023-08-15 12:34:56.789"`, `$.datetime()`, "2023-08-15T12:34:56.789", Timestamp, 745418096789000, 0},
		{`"2023-08-15 12:34:56.789+05:30"`, `$.datetime()`, "2023-08-15T12:34:56.789+05:30", Timestamptz, 745398296789000, -19800},
		{`"2023-08-15"`, `$.timestamp()`, "2023-08-15T00:00:00", Timestamp, 745372800000000, 0},
		{`"2023-08-15 12:34:56+05:30"`, `$.time_tz()`, "07:04:56+00:00", Timetz, 25496000000, 0},
	} {
		t.Run(tc.path+tc.doc, func(t *testing.T) {
			values := navigateValues(t, types, tc.doc, tc.path, `{}`, false)
			count := 0
			status, err := executeJSONPath(context.Background(), values[0], values[1], values[2], JSONPathEnvironment{}, func(item jsonpathItem) (bool, error) {
				count++
				if item.temporal == nil || item.temporal.typ.id != tc.id || item.temporal.integer != tc.integer {
					t.Fatalf("typed item=%+v want type=%d integer=%d", item.temporal, tc.id, tc.integer)
				}
				if tc.id == Timetz && item.temporal.west != tc.west {
					t.Fatal("timetz offset", item.temporal.west, tc.west)
				}
				if tc.id == Timestamptz && (!item.hasOffset || item.offsetSecondsWest != tc.west) {
					t.Fatal("lost parsed offset", item)
				}
				if got := jsonpathTemporalText(item); got != tc.text {
					t.Fatalf("text=%q want=%q", got, tc.text)
				}
				return true, nil
			})
			if err != nil || status.kind != jsonpathComplete || count != 1 {
				t.Fatal(status, err, count)
			}
		})
	}
}

func TestJSONPathDatetimeStrictReader(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		source, template string
		id               ID
		integer          int64
	}{
		{"15.08.2023", "DD.MM.YYYY", Date, 8627},
		{"2023-33-2", "IYYY-IW-ID", Date, 8627},
		{"12:30", "HH24:MI", Time, 45000000000},
		{"2023-00-15", "YYYY-MM-DD", Date, 8415},
		{"2023-08-00", "YYYY-MM-DD", Date, 8613},
	} {
		nodes, err := compileDatetime(ctx, tc.template, datetimeJSONPathFormat)
		if err != nil {
			t.Fatal(err)
		}
		value, err := datetimeInput(ctx, nil, tc.source, nodes, InputEnvironment{}, false, true, nil)
		if err != nil || value.typ.id != tc.id || value.integer != tc.integer {
			t.Fatalf("%s/%s => %+v %v", tc.source, tc.template, value, err)
		}
	}
	for _, tc := range []struct{ source, template, message, detail, hint string }{
		{"2023/08/15", "YYYY-MM-DD", `unmatched format separator "-"`, "", ""},
		{"2023-08", "YYYY-MM-DD", `input string is too short for datetime format`, "", ""},
		{"2023-08-15x", "YYYY-MM-DD", `trailing characters remain in input string after datetime format`, "", ""},
		{"2023_08_15", "YYYY_MM_DD", `invalid datetime format separator: "_"`, "", ""},
		{"2023Y08X15", `YYYY"X"MM"X"DD`, `unmatched format character "X"`, "", ""},
		{"2023 2024", "YYYY YYYY", `conflicting values for "YYYY" field in formatting string`, "This value contradicts a previous setting for the same field type.", ""},
		{"2023 33", "YYYY IW", `invalid combination of date conventions`, "", "Do not mix Gregorian and ISO week date conventions in a formatting template."},
		{"x", `"x"`, `datetime format is not dated and not timed`, "", ""},
	} {
		nodes, err := compileDatetime(ctx, tc.template, datetimeJSONPathFormat)
		if err == nil {
			_, err = datetimeInput(ctx, nil, tc.source, nodes, InputEnvironment{}, false, true, nil)
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: tc.message, Detail: tc.detail, Hint: tc.hint}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s/%s diagnostic=%+v want=%+v", tc.source, tc.template, got, want)
		}
	}
}

func TestJSONPathDatetimePrecisionAndNotices(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	for _, tc := range []struct {
		doc, path, want string
		count           int
	}{
		{`["12:34:56.123456","12:34:56.123456"]`, `$[*].time(10)`, `["12:34:56.123456", "12:34:56.123456"]`, 2},
		{`["12:34:56.123456","bad","12:34:56.123456"]`, `$[*].time(10)`, `["12:34:56.123456"]`, 1},
		{`[]`, `$[*].time(10)`, `[]`, 0},
		{`"1999-12-31 23:59:59.5"`, `$.timestamp(0)`, `["1999-12-31T23:59:59"]`, 0},
		{`"2000-01-01 00:00:00.5"`, `$.timestamp(0)`, `["2000-01-01T00:00:01"]`, 0},
		{`"23:59:59.999999"`, `$.time(0)`, `["24:00:00"]`, 0},
	} {
		var notices []sqlstate.Error
		values := navigateValues(t, types, tc.doc, tc.path, `{}`, true)
		value, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{Notice: func(n sqlstate.Error) { notices = append(notices, n) }})
		if err != nil || value.text != tc.want || len(notices) != tc.count {
			t.Fatal(tc.path, value.text, err, notices)
		}
		want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIME(10) precision reduced to maximum allowed, 6"}
		for _, notice := range notices {
			if notice != want {
				t.Fatalf("notice=%+v want=%+v", notice, want)
			}
		}
	}
}

func TestJSONPathDatetimeInvocationIsolation(t *testing.T) {
	types := testTypes()
	plan := navigatePlan(t, types)
	values := navigateValues(t, types, `"2023-08-15 12:34:56+05:30"`, `$.time_tz()`, `{}`, false)
	var workers sync.WaitGroup
	for _, tc := range []struct {
		east int32
		want string
	}{{0, `["07:04:56+00:00"]`}, {36000, `["17:04:56+10:00"]`}, {-36000, `["21:04:56-10:00"]`}} {
		workers.Go(func() {
			for range 10 {
				value, err := plan.Evaluate(context.Background(), values, JSONPathEnvironment{Input: InputEnvironment{Zone: FixedZone("unit", tc.east)}})
				if err != nil || value.text != tc.want {
					t.Errorf("zone=%d output=%s error=%v", tc.east, value.text, err)
					return
				}
			}
		})
	}
	workers.Wait()
	for _, path := range []string{`$.datetime("YYYY-MM-DD")`, `$.datetime()`} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := plan.Evaluate(ctx, navigateValues(t, types, `"2023-08-15"`, path, `{}`, false), JSONPathEnvironment{})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	ctx := &groupingCancelContext{Context: context.Background(), at: 4}
	nodes, err := compileDatetime(context.Background(), strings.Repeat(`"x"`, 4096)+"YYYY", datetimeJSONPathFormat)
	if err != nil {
		t.Fatal(err)
	}
	_, err = datetimeInput(ctx, nil, strings.Repeat("x", 4096)+"2023", nodes, InputEnvironment{}, false, true, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("strict reader did not poll", err)
	}
}

func TestJSONPathDatetimeComparisonPolicy(t *testing.T) {
	date := Value{typ: jsonpathDatetimeType(Date), integer: 8627}
	stamp := Value{typ: jsonpathDatetimeType(Timestamp), integer: 745372800000000}
	zoned := Value{typ: jsonpathDatetimeType(Timestamptz), integer: 745372800000000}
	e := jsonpathExecutor{ctx: context.Background()}
	if cmp, ok, err := e.temporalCompare(date, stamp); err != nil || !ok || cmp != 0 {
		t.Fatal(cmp, ok, err)
	}
	_, _, err := e.temporalCompare(date, zoned)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot convert value from date to timestamptz without time zone usage", Hint: "Use *_tz() function for time zone support."}
	var got *sqlstate.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatal(got, err)
	}
	e.timezone = true
	if cmp, ok, err := e.temporalCompare(date, zoned); err != nil || !ok || cmp != 0 {
		t.Fatal(cmp, ok, err)
	}
}
