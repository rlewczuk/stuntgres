package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestTimestamptzRangeCanonicalDatum(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[TimestamptzRange]
	if TimestamptzRange != 3910 || !d.Concrete() || d.rangeSubtype != types[Timestamptz] || d.AssociatedArray() != 3911 || types[3911].Concrete() || types[4534].Concrete() || d.multirangeCompanion != nil || StoredScalarCastType(TimestamptzRange) {
		t.Fatal("timestamp range authority", d)
	}
	for _, tc := range []struct {
		input, output string
		datum         rangeDatum
	}{
		{"(2000-01-01,2000-01-02]", `("2000-01-01 00:00:00+00","2000-01-02 00:00:00+00"]`, rangeDatum{rangeBound{magnitude: 0}, rangeBound{magnitude: microsPerDay}, rangeUpperInclusive}},
		{"(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)", `("2000-01-01 00:00:00+00","2000-01-01 00:00:00.000001+00")`, rangeDatum{rangeBound{}, rangeBound{magnitude: 1}, 0}},
		{"[2000-01-01,2000-01-01]", `["2000-01-01 00:00:00+00","2000-01-01 00:00:00+00"]`, rangeDatum{flags: rangeLowerInclusive | rangeUpperInclusive}},
		{"[2000-01-01,2000-01-01)", "empty", rangeDatum{flags: rangeEmpty}},
		{"(2000-01-01,2000-01-01]", "empty", rangeDatum{flags: rangeEmpty}},
		{"(2000-01-01,2000-01-01)", "empty", rangeDatum{flags: rangeEmpty}},
		{"(4714-11-24 00:00:00 BC,294276-12-31 23:59:59.999999]", `("4714-11-24 00:00:00+00 BC","294276-12-31 23:59:59.999999+00"]`, rangeDatum{rangeBound{magnitude: timestampMinimum}, rangeBound{magnitude: timestampEnd - 1}, rangeUpperInclusive}},
		{"[-infinity,infinity]", "[-infinity,infinity]", rangeDatum{rangeBound{magnitude: TimestampNegativeInfinity}, rangeBound{magnitude: TimestampPositiveInfinity}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[infinity,infinity]", "[infinity,infinity]", rangeDatum{rangeBound{magnitude: TimestampPositiveInfinity}, rangeBound{magnitude: TimestampPositiveInfinity}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[-infinity,-infinity]", "[-infinity,-infinity]", rangeDatum{rangeBound{magnitude: TimestampNegativeInfinity}, rangeBound{magnitude: TimestampNegativeInfinity}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[infinity,infinity)", "empty", rangeDatum{flags: rangeEmpty}},
		{"(infinity,)", "(infinity,)", rangeDatum{lower: rangeBound{magnitude: TimestampPositiveInfinity}, flags: rangeUpperUnbounded}},
		{"(,-infinity)", "(,-infinity)", rangeDatum{upper: rangeBound{magnitude: TimestampNegativeInfinity}, flags: rangeLowerUnbounded}},
		{"[,]", "(,)", rangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
		{"empty", "empty", rangeDatum{flags: rangeEmpty}},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || !v.validRange() || v.rangeDatum != tc.datum {
			t.Fatalf("%q datum=%#v want=%#v err=%v", tc.input, v.rangeDatum, tc.datum, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output {
			t.Fatal(tc.input, out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'" {
			t.Fatal(literal, err)
		}
		again, err := d.Input(ctx, out, -1)
		if err != nil || !SameDatum(v, again) {
			t.Fatal("non-idempotent", tc.input, err)
		}
	}
	n, err := types[NumRange].Input(ctx, "(4,5)", -1)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := types[NumRange].Output(ctx, n); err != nil || out != "(4,5)" {
		t.Fatal("numeric no longer continuous", out, err)
	}
	for _, tc := range []struct{ input, code, message string }{
		{"[2000-01-02,2000-01-01)", "22000", "range lower bound must be less than or equal to range upper bound"},
		{"[bad,294277-01-01)", "22007", `invalid input syntax for type timestamp with time zone: "bad"`},
		{"[294277-01-01,infinity)", "22008", `timestamp out of range: "294277-01-01"`},
	} {
		_, err := d.Input(ctx, tc.input, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%q diagnostic=%#v want=%#v err=%v", tc.input, got, want, err)
		}
	}
}

func TestTimestamptzRangeOwnershipValidation(t *testing.T) {
	ctx := context.Background()
	d, foreign := testTypes()[TimestamptzRange], testTypes()[TimestamptzRange]
	v, err := d.Input(ctx, "[2000-01-01,infinity]", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Output(ctx, v); err == nil {
		t.Fatal("foreign output")
	}
	if _, err := foreign.SQLLiteral(ctx, v); err == nil {
		t.Fatal("foreign SQLLiteral")
	}
	retag, err := v.Retag(foreign)
	if err != nil || retag.Type() != foreign || retag.rangeDatum != v.rangeDatum {
		t.Fatal("retag", retag, err)
	}
	if _, err := v.Retag(testTypes()[DateRange]); err == nil {
		t.Fatal("unrelated retag")
	}
	for _, input := range []string{"[2000-01-01,infinity)", "[2000-01-01,)"} {
		other, err := d.Input(ctx, input, -1)
		if err != nil || SameDatum(v, other) {
			t.Fatal("infinity flags/omission collapsed", input, err)
		}
	}
	for _, bad := range []rangeDatum{
		{rangeBound{}, rangeBound{}, 0},
		{rangeBound{magnitude: 2}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: timestampMinimum - 1}, rangeBound{magnitude: 1}, 0},
		{rangeBound{}, rangeBound{magnitude: timestampEnd + 1}, 0},
		{lower: rangeBound{magnitude: 1}, flags: rangeEmpty},
		{lower: rangeBound{magnitude: 1}, flags: rangeLowerUnbounded | rangeUpperUnbounded},
		{upper: rangeBound{magnitude: 1}, flags: rangeUpperUnbounded},
		{flags: rangeLowerUnbounded | rangeUpperUnbounded | rangeLowerInclusive},
		{flags: rangeLowerUnbounded | rangeUpperUnbounded | rangeUpperInclusive},
		{flags: 128},
		{rangeBound{scale: 1}, rangeBound{magnitude: 1}, 0},
		{rangeBound{coefficient: "1"}, rangeBound{magnitude: 1}, 0},
		{rangeBound{negative: true}, rangeBound{magnitude: 1}, 0},
		{rangeBound{special: 1}, rangeBound{magnitude: 1}, 0},
	} {
		invalid := Value{typ: d, rangeDatum: bad}
		if _, err := d.Output(ctx, invalid); err == nil || SameDatum(invalid, invalid) {
			t.Fatal("malformed", bad, err)
		}
		if _, err := invalid.Retag(foreign); err == nil {
			t.Fatal("malformed retag", bad)
		}
	}
	// Follow the scalar constructor's internal validity boundary exactly; text
	// input's external end rejection is separately specified by reference tests.
	for _, n := range []int64{timestampMinimum, timestampEnd, TimestampNegativeInfinity, TimestampPositiveInfinity} {
		scalar, err := TimestampValue(d.rangeSubtype, n)
		if err != nil {
			t.Fatal(err)
		}
		if !d.validRangeDatum(rangeDatum{lower: rangeBound{magnitude: scalar.integer}, flags: rangeUpperUnbounded}) {
			t.Fatal("scalar boundary rejected", n)
		}
	}
	if _, err := d.Output(ctx, Null(d)); err == nil {
		t.Fatal("NULL output")
	}
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" {
		t.Fatal(literal, err)
	}
	for _, input := range []string{"[epoch,epoch]\x00", "[\xff,epoch)"} {
		if _, err := d.Input(ctx, input, -1); err == nil {
			t.Fatal("invalid bytes", input)
		}
	}
	if _, err := d.Input(ctx, "[epoch,epoch]", 0); err == nil {
		t.Fatal("modifier")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, call := range []func() error{
		func() error { _, err := d.Input(canceled, "[epoch,epoch]", -1); return err },
		func() error { _, err := d.Output(canceled, v); return err },
		func() error { _, err := d.SQLLiteral(canceled, v); return err },
	} {
		if !errors.Is(call(), context.Canceled) {
			t.Fatal("cancellation")
		}
	}
	for _, bound := range []string{strings.Repeat("x", 4096), strings.Repeat(`\x`, 4096)} {
		ctx := &binaryEncodingCancelContext{Context: context.Background(), cancelAt: 2}
		value, err := d.rangeInput(ctx, "["+bound+",epoch)", InputEnvironment{})
		if !errors.Is(err, context.Canceled) || value != (Value{}) {
			t.Fatal("canceled scan published", value, err)
		}
	}
}

func TestTimestamptzRangeEnvironments(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[TimestamptzRange]
	for _, tc := range []struct {
		order  DateOrder
		output string
	}{
		{DateMDY, `["2003-01-02 00:00:00+00","2003-01-03 00:00:00+00")`},
		{DateDMY, `["2003-02-01 00:00:00+00","2003-03-01 00:00:00+00")`},
	} {
		v, err := d.InputWithEnvironment(ctx, "[01/02/2003,01/03/2003)", -1, InputEnvironment{DateStyle: DateStyle{Order: tc.order}})
		if err != nil {
			t.Fatal(err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc.output {
			t.Fatal(out, err)
		}
	}
	v, err := d.InputWithEnvironment(ctx, "[now,tomorrow)", -1, InputEnvironment{TransactionStart: time.Date(2000, 2, 29, 12, 34, 56, 123456000, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := d.Output(ctx, v); err != nil || out != `["2000-02-29 12:34:56.123456+00","2000-03-01 00:00:00+00")` {
		t.Fatal(out, err)
	}
	out, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{DateStyle: DateStyle{Style: DateGerman, Order: DateDMY}})
	if err != nil || out != `["29.02.2000 12:34:56.123456 UTC","01.03.2000 00:00:00 UTC")` {
		t.Fatal(out, err)
	}
	if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != `'["2000-02-29 12:34:56.123456+00","2000-03-01 00:00:00+00")'` {
		t.Fatal("ISO literal", literal, err)
	}
	zone, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	instant, err := d.Input(ctx, "[2010-01-01 01:00:00-05,2010-01-01 02:00:00-08)", -1)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := d.Input(ctx, "[2010-01-01 06:00:00+00,2010-01-01 10:00:00+00)", -1)
	if err != nil || !SameDatum(instant, equivalent) {
		t.Fatal("UTC normalization", err)
	}
	if out, err := d.OutputWithEnvironment(ctx, instant, OutputEnvironment{Zone: zone}); err != nil || out != `["2010-01-01 01:00:00-05","2010-01-01 05:00:00-05")` {
		t.Fatal(out, err)
	}
	if _, err := d.Input(ctx, "[2010-01-01 01:00:00-08,2010-01-01 02:00:00-05)", -1); err == nil {
		t.Fatal("UTC reversal admitted")
	}
}

func TestTimestamptzRangeBinaryDatum(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[TimestamptzRange]
	for _, tc := range []struct{ payload, text string }{
		{"00000000080000000000000000000000080000000000000001", `("2000-01-01 00:00:00+00","2000-01-01 00:00:00.000001+00")`},
		{"06000000088000000000000000000000087fffffffffffffff", "[-infinity,infinity]"},
		{"18", "(,)"}, {"01", "empty"},
	} {
		payload, err := hex.DecodeString(tc.payload)
		if err != nil {
			t.Fatal(err)
		}
		v, consumed, err := d.binaryReceive(ctx, payload, -1, InputEnvironment{})
		if err != nil || consumed != len(payload) || !v.validRange() {
			t.Fatal(tc.payload, consumed, err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc.text {
			t.Fatal(out, err)
		}
		encoded, err := d.appendBinaryRange(ctx, nil, v.rangeDatum, OutputEnvironment{})
		if err != nil || hex.EncodeToString(encoded) != tc.payload {
			t.Fatal("binary flags/payload", hex.EncodeToString(encoded), err)
		}
	}
}

func TestTimestamptzRangeComparisonAdmission(t *testing.T) {
	types := testTypes()
	d := types[TimestamptzRange]
	domain, err := Domain(20000, "span", "span", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Domain(20001, "chain", "chain", domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, operand := range []*Descriptor{d, domain, nested} {
		for op := CompareEqual; op <= CompareGreaterEqual; op++ {
			_, err := BindComparisonPredicate(op, operand, operand, types[Bool], CollationRef{}, false)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
				t.Fatal("public comparison admitted", operand.Name(), op, err)
			}
		}
	}
}
