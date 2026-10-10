package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestDatetimeExtractProviderOwnershipAndCancellation(t *testing.T) {
	d, next := datetimeMakeDescriptors(), datetimeMakeDescriptors()
	zone, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{Date, Time, Timetz, Timestamp, Timestamptz, Interval} {
		for _, api := range []string{"extract", "date_part"} {
			result := Numeric
			if api == "date_part" {
				result = Float8
			}
			plan, err := BindDatetimeExtract(api, d[Text], d[id], d[result], d[Timestamp])
			if err != nil {
				t.Fatal(err)
			}
			retagged, err := plan.Retag(func(old *Descriptor) *Descriptor { return next[old.ID()] })
			if err != nil {
				t.Fatal(err)
			}
			if retagged.input == plan.input || retagged.field == plan.field || retagged.result == plan.result || retagged.SessionDependent() != (id == Timestamptz) {
				t.Fatal("retained provider authority", id, api)
			}
			if id == Date && api == "date_part" && (retagged.dateCast.target.Type != next[Timestamp] || retagged.timestamp == plan.timestamp) {
				t.Fatal("DATE cast authority not rebuilt")
			}
			args := [2]Value{String(next[Text], "epoch"), {typ: next[id]}}
			v, err := retagged.Evaluate(context.Background(), args[:], InputEnvironment{Zone: zone})
			if err != nil || v.Type() != next[result] {
				t.Fatal(id, api, v, err)
			}
			args[0] = String(d[Text], "epoch")
			if _, err := retagged.Evaluate(context.Background(), args[:], InputEnvironment{Zone: zone}); err == nil {
				t.Fatal("foreign field descriptor accepted")
			}
			args[0] = String(next[Text], "bogus")
			args[1] = Null(next[id])
			v, err = retagged.Evaluate(context.Background(), args[:], InputEnvironment{Zone: zone})
			if err != nil || !v.IsNull() || v.Type() != next[result] {
				t.Fatal(v, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := retagged.Evaluate(ctx, args[:], InputEnvironment{Zone: zone}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}

func TestDatetimeExtractStoredTupleArithmetic(t *testing.T) {
	d := datetimeMakeDescriptors()
	plan, err := BindDatetimeExtract("extract", d[Text], d[Interval], d[Numeric], d[Timestamp])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		field        string
		months, days int32
		micros       int64
		want         string
	}{
		{"quarter", math.MinInt32, 0, 0, "1"},
		{"epoch", math.MaxInt32, math.MinInt32, -math.MaxInt64, "5452686535391945.224193"},
		{"epoch", 0, 0, math.MinInt64, "-9223372036854.775808"},
		{"epoch", 0, 0, math.MaxInt64, "9223372036854.775807"},
		{"second", 0, 0, math.MinInt64, "-54.775808"},
		{"minute", 0, 0, math.MinInt64, "0"},
		{"epoch", -11, -1, 1, "-28598399.999999"},
	} {
		v, err := IntervalValue(d[Interval], tc.months, tc.days, tc.micros)
		if err != nil {
			t.Fatal(err)
		}
		args := [2]Value{String(d[Text], tc.field), v}
		got, err := plan.Evaluate(context.Background(), args[:], InputEnvironment{})
		if err != nil || numericOutput(got) != tc.want {
			t.Fatal(tc, numericOutput(got), err)
		}
	}
}

func TestDatetimeExtractTimestampEpochBranchBoundary(t *testing.T) {
	d := datetimeMakeDescriptors()
	plan, err := BindDatetimeExtract("extract", d[Text], d[Timestamp], d[Numeric], d[Timestamp])
	if err != nil {
		t.Fatal(err)
	}
	const boundary = math.MaxInt64 - 946684800000000
	for _, tc := range []struct {
		micros int64
		want   string
	}{{boundary - 1, "9223372036854.775806"}, {boundary, "9223372036854.775800"}, {boundary + 1, "9223372036854.775800"}} {
		v, err := TimestampValue(d[Timestamp], tc.micros)
		if err != nil {
			t.Fatal(err)
		}
		args := [2]Value{String(d[Text], "epoch"), v}
		got, err := plan.Evaluate(context.Background(), args[:], InputEnvironment{})
		if err != nil || numericOutput(got) != tc.want {
			t.Fatal(tc, numericOutput(got), err)
		}
	}
}

func TestDatetimeExtractNumericDivisionScale(t *testing.T) {
	d := datetimeMakeDescriptors()[Numeric]
	for _, tc := range []struct {
		numerator int64
		want      string
		scale     int64
	}{{0, "0.0000000000000000000000000000", 28}, {1, "0.0000000000115740740740740741", 28}, {43200000000, "0.50000000000000000000", 20}, {86399999999, "0.99999999998842592593", 20}} {
		got := datetimeExtractDivide(d, tc.numerator, 86400000000)
		if numericOutput(got) != tc.want || got.scale != tc.scale {
			t.Fatal(tc, numericOutput(got), got.scale)
		}
	}
}

func TestDatetimeExtractUnitNormalization(t *testing.T) {
	for _, tc := range []struct{ input, normalized, unit string }{{"m", "m", "minute"}, {"mm", "mm", "minute"}, {"mon", "mon", "month"}, {"MiNuTeS", "minutes", "minute"}, {"microseconds_extra", "microseconds_extra", "microseconds"}, {"timezone_hour_extra", "timezone_hour_extra", "timezone_hour"}, {"year_extra", "year_extra", ""}, {" year", " year", ""}, {"ÉYEAR", "Éyear", ""}} {
		normalized, unit := datetimeExtractUnit(tc.input)
		if normalized != tc.normalized || unit != tc.unit {
			t.Fatal(tc, normalized, unit)
		}
	}
}

func TestDatetimeExtractBCEraBoundaries(t *testing.T) {
	d := datetimeMakeDescriptors()
	plan, err := BindDatetimeExtract("extract", d[Text], d[Date], d[Numeric], d[Timestamp])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		astronomicalYear int
		field, want      string
	}{
		{0, "year", "-1"}, {0, "century", "-1"}, {0, "millennium", "-1"}, {0, "decade", "0"},
		{-1, "decade", "-1"}, {-99, "century", "-1"}, {-100, "century", "-2"},
		{-999, "millennium", "-1"}, {-1000, "millennium", "-2"},
		{-9, "decade", "-1"}, {-10, "decade", "-1"}, {-11, "decade", "-2"},
	} {
		date, err := DateValue(d[Date], civilDay(tc.astronomicalYear, 1, 1))
		if err != nil {
			t.Fatal(err)
		}
		args := [2]Value{String(d[Text], tc.field), date}
		value, err := plan.Evaluate(context.Background(), args[:], InputEnvironment{})
		if err != nil || numericOutput(value) != tc.want {
			t.Fatal(tc, numericOutput(value), err)
		}
	}
}
