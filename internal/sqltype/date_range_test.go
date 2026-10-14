package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDateRangeCanonicalDatum(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[DateRange]
	if DateRange != 3912 || !d.Concrete() || d.rangeSubtype != types[Date] || d.AssociatedArray() != 3913 || types[3913].Concrete() || types[4535].Concrete() || d.multirangeCompanion != nil || StoredScalarCastType(DateRange) {
		t.Fatal("daterange descriptor/subtype/array/companion/cast authority", d)
	}
	if types[3910].Concrete() { t.Fatal("tstzrange admitted") }
	for _, tc := range []struct { input, output string; datum rangeDatum }{
		{"[2000-02-28,2000-03-01]", "[2000-02-28,2000-03-02)", rangeDatum{rangeBound{magnitude: 58}, rangeBound{magnitude: 61}, rangeLowerInclusive}},
		{"(2000-02-28,2000-03-01)", "[2000-02-29,2000-03-01)", rangeDatum{rangeBound{magnitude: 59}, rangeBound{magnitude: 60}, rangeLowerInclusive}},
		{"[2000-02-29,2000-02-29]", "[2000-02-29,2000-03-01)", rangeDatum{rangeBound{magnitude: 59}, rangeBound{magnitude: 60}, rangeLowerInclusive}},
		{"(2000-02-28,2000-02-29)", "empty", rangeDatum{flags: rangeEmpty}},
		{"[5874897-12-31,5874897-12-31)", "empty", rangeDatum{flags: rangeEmpty}},
		{"(5874897-12-31,5874897-12-31]", "empty", rangeDatum{flags: rangeEmpty}},
		{`["4714-11-24 BC",5874897-12-31)`, `["4714-11-24 BC",5874897-12-31)`, rangeDatum{rangeBound{magnitude: -2451545}, rangeBound{magnitude: 2145031948}, rangeLowerInclusive}},
		{"[5874897-12-31,)", "[5874897-12-31,)", rangeDatum{lower: rangeBound{magnitude: 2145031948}, flags: rangeLowerInclusive|rangeUpperUnbounded}},
		{"[-infinity,infinity]", "[-infinity,infinity]", rangeDatum{rangeBound{magnitude: DateNegativeInfinity}, rangeBound{magnitude: DatePositiveInfinity}, rangeLowerInclusive|rangeUpperInclusive}},
		{"[infinity,infinity]", "[infinity,infinity]", rangeDatum{rangeBound{magnitude: DatePositiveInfinity}, rangeBound{magnitude: DatePositiveInfinity}, rangeLowerInclusive|rangeUpperInclusive}},
		{"[-infinity,-infinity]", "[-infinity,-infinity]", rangeDatum{rangeBound{magnitude: DateNegativeInfinity}, rangeBound{magnitude: DateNegativeInfinity}, rangeLowerInclusive|rangeUpperInclusive}},
		{"[infinity,infinity)", "empty", rangeDatum{flags: rangeEmpty}},
		{"(infinity,)", "(infinity,)", rangeDatum{lower: rangeBound{magnitude: DatePositiveInfinity}, flags: rangeUpperUnbounded}},
		{"(,-infinity)", "(,-infinity)", rangeDatum{upper: rangeBound{magnitude: DateNegativeInfinity}, flags: rangeLowerUnbounded}},
		{"[,]", "(,)", rangeDatum{flags: rangeLowerUnbounded|rangeUpperUnbounded}},
		{"empty", "empty", rangeDatum{flags: rangeEmpty}},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || !v.validRange() || v.rangeDatum != tc.datum { t.Fatalf("%q datum=%#v want=%#v err=%v", tc.input, v.rangeDatum, tc.datum, err) }
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
		again, err := d.Input(ctx, out, -1)
		if err != nil || !SameDatum(v, again) { t.Fatal("non-idempotent canonicalization", tc.input, err) }
	}
	for _, tc := range []struct{ input, code, message string }{
		{"[5874897-12-31,5874897-12-31]", "22008", "date out of range"},
		{"[2000-01-01,5874897-12-31]", "22008", "date out of range"},
		{"(5874897-12-31,)", "22008", "date out of range"},
		{`[5874897-12-31,"4714-11-24 BC"]`, "22000", "range lower bound must be less than or equal to range upper bound"},
		{"[bad,5874898-01-01]", "22007", `invalid input syntax for type date: "bad"`},
		{"(5874897-12-31,bad]", "22007", `invalid input syntax for type date: "bad"`},
	} {
		_, err := d.Input(ctx, tc.input, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want { t.Fatalf("%q got=%#v want=%#v err=%v", tc.input, got, want, err) }
	}
}

func TestDateRangeOwnershipValidation(t *testing.T) {
	ctx := context.Background()
	d, foreign := testTypes()[DateRange], testTypes()[DateRange]
	v, err := d.Input(ctx, "[2000-01-01,infinity]", -1)
	if err != nil { t.Fatal(err) }
	if _, err := foreign.Output(ctx, v); err == nil { t.Fatal("foreign output") }
	if _, err := foreign.SQLLiteral(ctx, v); err == nil { t.Fatal("foreign SQLLiteral") }
	retag, err := v.Retag(foreign)
	if err != nil || retag.Type() != foreign || retag.rangeDatum != v.rangeDatum { t.Fatal("retag", retag, err) }
	if _, err := v.Retag(testTypes()[Int4Range]); err == nil { t.Fatal("wrong ID retag") }
	for _, input := range []string{"[2000-01-01,infinity)", "[2000-01-01,)"} {
		other, err := d.Input(ctx, input, -1)
		if err != nil || SameDatum(v, other) { t.Fatal("infinity inclusivity/omission collapsed", input, err) }
	}
	for _, bad := range []rangeDatum{
		{rangeBound{magnitude: 0}, rangeBound{magnitude: 1}, 0},
		{rangeBound{magnitude: 0}, rangeBound{magnitude: 1}, rangeLowerInclusive|rangeUpperInclusive},
		{rangeBound{magnitude: 0}, rangeBound{magnitude: 0}, rangeLowerInclusive},
		{rangeBound{magnitude: 2}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: dateMinimum-1}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: 0}, rangeBound{magnitude: dateMaximum+1}, rangeLowerInclusive},
		{lower: rangeBound{magnitude: 1}, flags: rangeEmpty},
		{lower: rangeBound{magnitude: 1}, flags: rangeLowerUnbounded|rangeUpperUnbounded},
		{upper: rangeBound{magnitude: 1}, flags: rangeLowerInclusive|rangeUpperUnbounded},
		{flags: rangeLowerUnbounded|rangeUpperUnbounded|rangeLowerInclusive},
		{flags: 128},
		{rangeBound{magnitude: 0, scale: 1}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: 0, coefficient: "1"}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: 0, negative: true}, rangeBound{magnitude: 1}, rangeLowerInclusive},
		{rangeBound{magnitude: 0, special: 1}, rangeBound{magnitude: 1}, rangeLowerInclusive},
	} {
		invalid := Value{typ: d, rangeDatum: bad}
		if _, err := d.Output(ctx, invalid); err == nil || SameDatum(invalid, invalid) { t.Fatal("malformed datum", bad, err) }
		if _, err := invalid.Retag(foreign); err == nil { t.Fatal("malformed retag", bad) }
	}
	if _, err := d.Output(ctx, Null(d)); err == nil { t.Fatal("NULL Output") }
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" { t.Fatal(literal, err) }
	for _, input := range []string{"[epoch,epoch]\x00", "[\xff,epoch)"} { if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid bytes", input) } }
	if _, err := d.Input(ctx, "[epoch,epoch]", 0); err == nil { t.Fatal("modifier accepted") }
	canceled, cancel := context.WithCancel(ctx); cancel()
	for _, call := range []func() error{
		func() error { _, err := d.Input(canceled, "[epoch,epoch]", -1); return err },
		func() error { _, err := d.Output(canceled, v); return err },
		func() error { _, err := d.SQLLiteral(canceled, v); return err },
	} { if !errors.Is(call(), context.Canceled) { t.Fatal("cancellation") } }
}

func TestDateRangeEnvironments(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[DateRange]
	for _, tc := range []struct{ order DateOrder; output string }{
		{DateMDY, "[2003-01-02,2003-01-03)"}, {DateDMY, "[2003-02-01,2003-03-01)"},
	} {
		v, err := d.InputWithEnvironment(ctx, "[01/02/03,01/03/03)", -1, InputEnvironment{DateStyle: DateStyle{Style: DateISO, Order: tc.order}})
		if err != nil { t.Fatal(err) }
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output { t.Fatal(out, err) }
	}
	v, err := d.Input(ctx, "[0001-12-31 BC,0001-01-01)", -1)
	if err != nil { t.Fatal(err) }
	out, err := d.OutputWithEnvironment(ctx, v, OutputEnvironment{DateStyle: DateStyle{Style: DateSQL, Order: DateDMY}})
	if err != nil || out != `["31/12/0001 BC",01/01/0001)` { t.Fatal(out, err) }
	literal, err := d.SQLLiteral(ctx, v)
	if err != nil || literal != `'["0001-12-31 BC",0001-01-01)'` { t.Fatal("ISO dump authority", literal, err) }
}

func TestDateRangeBinaryDatum(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[DateRange]
	for _, tc := range []struct{ input, canonical, text string }{
		{"0600000004000000000000000400000000", "0200000004000000000000000400000001", "[2000-01-01,2000-01-02)"},
		{"060000000480000000000000047fffffff", "060000000480000000000000047fffffff", "[-infinity,infinity]"},
		{"18", "18", "(,)"}, {"01", "01", "empty"},
	} {
		payload, err := hex.DecodeString(tc.input); if err != nil { t.Fatal(err) }
		v, consumed, err := d.binaryReceive(ctx, payload, -1, InputEnvironment{})
		if err != nil || consumed != len(payload) || !v.validRange() { t.Fatal(tc.input, consumed, err) }
		out, err := d.Output(ctx, v); if err != nil || out != tc.text { t.Fatal(out, err) }
		encoded, err := d.appendBinaryRange(ctx, nil, v.rangeDatum, OutputEnvironment{})
		if err != nil || hex.EncodeToString(encoded) != tc.canonical { t.Fatal("binary canonical", hex.EncodeToString(encoded), err) }
	}
}

func TestDateRangeCanceledBoundScan(t *testing.T) {
	d := testTypes()[DateRange]
	for _, bound := range []string{strings.Repeat("x", 4096), strings.Repeat(`\x`, 4096)} {
		ctx := &binaryEncodingCancelContext{Context: context.Background(), cancelAt: 2}
		value, err := d.rangeInput(ctx, "["+bound+",epoch)", InputEnvironment{})
		if !errors.Is(err, context.Canceled) || value != (Value{}) {
			t.Fatal("canceled bound scan published a datum", value, err)
		}
	}
}
