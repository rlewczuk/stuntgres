package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntegerRangeCanonicalDatum(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			types := testTypes()
			d := types[id]
			subtype := Int4
			if id == Int8Range {
				subtype = Int8
			}
			if !d.Concrete() || d.rangeSubtype != types[subtype] || StoredScalarCastType(id) || types[d.AssociatedArray()].Concrete() {
				t.Fatal("generation/subtype/array/cast admission", d)
			}
			for _, tc := range []struct {
				input, output string
				datum         rangeDatum
			}{
				{"[1,4]", "[1,5)", rangeDatum{rangeBound{magnitude: 1}, rangeBound{magnitude: 5}, rangeLowerInclusive}},
				{"(1,4)", "[2,4)", rangeDatum{rangeBound{magnitude: 2}, rangeBound{magnitude: 4}, rangeLowerInclusive}},
				{"(1,4]", "[2,5)", rangeDatum{rangeBound{magnitude: 2}, rangeBound{magnitude: 5}, rangeLowerInclusive}},
				{"[4,4]", "[4,5)", rangeDatum{rangeBound{magnitude: 4}, rangeBound{magnitude: 5}, rangeLowerInclusive}},
				{"[4,4)", "empty", rangeDatum{flags: rangeEmpty}},
				{"(4,4]", "empty", rangeDatum{flags: rangeEmpty}},
				{"(4,4)", "empty", rangeDatum{flags: rangeEmpty}},
				{"(4,5)", "empty", rangeDatum{flags: rangeEmpty}},
				{" \tEMPTY\r\n", "empty", rangeDatum{flags: rangeEmpty}},
				{"[,]", "(,)", rangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"(,]", "(,)", rangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"[,)", "(,)", rangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"[,4]", "(,5)", rangeDatum{upper: rangeBound{magnitude: 5}, flags: rangeLowerUnbounded}},
				{"[1,]", "[1,)", rangeDatum{lower: rangeBound{magnitude: 1}, flags: rangeLowerInclusive | rangeUpperUnbounded}},
				{" [ +0001 , 0x_F ] ", "[1,16)", rangeDatum{rangeBound{magnitude: 1}, rangeBound{magnitude: 16}, rangeLowerInclusive}},
				{`["1","4"]`, "[1,5)", rangeDatum{rangeBound{magnitude: 1}, rangeBound{magnitude: 5}, rangeLowerInclusive}},
				{`["1"2,14)`, "[12,14)", rangeDatum{rangeBound{magnitude: 12}, rangeBound{magnitude: 14}, rangeLowerInclusive}},
				{`[\1,4]`, "[1,5)", rangeDatum{rangeBound{magnitude: 1}, rangeBound{magnitude: 5}, rangeLowerInclusive}},
			} {
				v, err := d.Input(ctx, tc.input, -1)
				if err != nil || v.IsNull() || !v.validRange() || v.rangeDatum != tc.datum {
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
				canonical, err := d.Input(ctx, tc.output, -1)
				if err != nil || !SameDatum(v, canonical) {
					t.Fatal("canonical identity", tc.input, err)
				}
			}
			minimum, maximum := int64(math.MinInt32), int64(math.MaxInt32)
			if id == Int8Range {
				minimum, maximum = math.MinInt64, math.MaxInt64
			}
			for _, tc := range []struct {
				input string
				datum rangeDatum
			}{
				{fmt.Sprintf("[%d,%d)", minimum, maximum), rangeDatum{rangeBound{magnitude: minimum}, rangeBound{magnitude: maximum}, rangeLowerInclusive}},
				{fmt.Sprintf("(%d,%d)", minimum, maximum), rangeDatum{rangeBound{magnitude: minimum + 1}, rangeBound{magnitude: maximum}, rangeLowerInclusive}},
				{fmt.Sprintf("[%d,)", maximum), rangeDatum{lower: rangeBound{magnitude: maximum}, flags: rangeLowerInclusive | rangeUpperUnbounded}},
				{fmt.Sprintf("(,%d)", minimum), rangeDatum{upper: rangeBound{magnitude: minimum}, flags: rangeLowerUnbounded}},
				{fmt.Sprintf("[%d,%d)", maximum, maximum), rangeDatum{flags: rangeEmpty}},
				{fmt.Sprintf("(%d,%d]", maximum, maximum), rangeDatum{flags: rangeEmpty}},
			} {
				v, err := d.Input(ctx, tc.input, -1)
				if err != nil || v.rangeDatum != tc.datum {
					t.Fatal(tc.input, v.rangeDatum, err)
				}
			}
		})
	}
}

func TestIntegerRangeDiagnostics(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range} {
		d := testTypes()[id]
		name, maximum, minimum, overflow := "integer", "2147483647", "-2147483648", "2147483648"
		if id == Int8Range {
			name, maximum, minimum, overflow = "bigint", "9223372036854775807", "-9223372036854775808", "9223372036854775808"
		}
		for _, tc := range []struct{ input, code, message, detail string }{
			{"", "22P02", `malformed range literal: ""`, "Missing left parenthesis or bracket."},
			{"emptyx", "22P02", `malformed range literal: "emptyx"`, `Junk after "empty" key word.`},
			{"[1)", "22P02", `malformed range literal: "[1)"`, "Missing comma after lower bound."},
			{"[1,2,3]", "22P02", `malformed range literal: "[1,2,3]"`, "Too many commas."},
			{"[oops,4", "22P02", `malformed range literal: "[oops,4"`, "Unexpected end of input."},
			{"[oops,4)junk", "22P02", `malformed range literal: "[oops,4)junk"`, "Junk after right parenthesis or bracket."},
			{"[oops," + overflow + "]", "22P02", `invalid input syntax for type ` + name + `: "oops"`, ""},
			{`["",4)`, "22P02", `invalid input syntax for type ` + name + `: ""`, ""},
			{"[ ,4)", "22P02", `invalid input syntax for type ` + name + `: " "`, ""},
			{"[NULL,4)", "22P02", `invalid input syntax for type ` + name + `: "NULL"`, ""},
			{"[1," + overflow + ")", "22003", `value "` + overflow + `" is out of range for type ` + name, ""},
			{"[1," + maximum + "]", "22003", name + " out of range", ""},
			{"[" + maximum + "," + maximum + "]", "22003", name + " out of range", ""},
			{"(" + maximum + ",)", "22003", name + " out of range", ""},
			{"[" + maximum + "," + minimum + "]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		} {
			_, err := d.Input(ctx, tc.input, -1)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("%d %q got=%#v want=%#v err=%v", id, tc.input, got, want, err)
			}
		}
	}
}

func TestIntegerRangeOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range} {
		d, foreign := testTypes()[id], testTypes()[id]
		v, err := d.Input(ctx, "[1,4]", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := foreign.Output(ctx, v); err == nil {
			t.Fatal("foreign output admitted")
		}
		if _, err := foreign.SQLLiteral(ctx, v); err == nil {
			t.Fatal("foreign literal admitted")
		}
		retag, err := v.Retag(foreign)
		if err != nil || retag.rangeDatum != v.rangeDatum || retag.Type() != foreign {
			t.Fatal(retag, err)
		}
		if _, err := v.Retag(testTypes()[Int4]); err == nil {
			t.Fatal("wrong identity retag")
		}
		for _, bad := range []rangeDatum{
			{rangeBound{magnitude: 1}, rangeBound{magnitude: 5}, 0},
			{rangeBound{magnitude: 1}, rangeBound{magnitude: 5}, rangeUpperInclusive},
			{lower: rangeBound{magnitude: 1}, flags: rangeLowerUnbounded},
			{upper: rangeBound{magnitude: 1}, flags: rangeUpperUnbounded},
			{lower: rangeBound{magnitude: 1}, flags: rangeEmpty},
			{rangeBound{magnitude: 5}, rangeBound{magnitude: 1}, rangeLowerInclusive},
			{rangeBound{magnitude: 1}, rangeBound{magnitude: 1}, rangeLowerInclusive},
			{rangeBound{magnitude: 1}, rangeBound{magnitude: 2}, 128},
			{rangeBound{coefficient: "1", magnitude: 1}, rangeBound{magnitude: 2}, rangeLowerInclusive},
		} {
			invalid := Value{typ: d, rangeDatum: bad}
			if _, err := d.Output(ctx, invalid); err == nil || SameDatum(invalid, invalid) {
				t.Fatal("invalid datum accepted", bad, err)
			}
			if _, err := invalid.Retag(foreign); err == nil {
				t.Fatal("invalid retag accepted", bad)
			}
		}
		if _, err := d.Output(ctx, Null(d)); err == nil {
			t.Fatal("NULL output")
		}
		if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" || SameDatum(Null(d), v) {
			t.Fatal(literal, err)
		}
		if _, err := d.Input(ctx, "[1,4]", 0); err == nil {
			t.Fatal("modifier accepted")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		for _, call := range []func() error{
			func() error { _, err := d.Input(canceled, "[1,4]", -1); return err },
			func() error { _, err := d.Output(canceled, v); return err },
			func() error { _, err := d.SQLLiteral(canceled, v); return err },
		} {
			if !errors.Is(call(), context.Canceled) {
				t.Fatal("cancellation lost")
			}
		}
		for _, input := range []string{"[1,4]\x00", "[\xff,4)"} {
			if _, err := d.Input(ctx, input, -1); err == nil {
				t.Fatal("invalid character input admitted")
			}
		}
	}
}

func TestNumericRangeCanonicalDatum(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[NumRange]
	if NumRange != 3906 || !d.Concrete() || d.rangeSubtype != types[Numeric] || d.AssociatedArray() != 3907 || types[3907].Concrete() || StoredScalarCastType(NumRange) {
		t.Fatal("generation/subtype/array/cast admission", d)
	}
	for _, tc := range []struct {
		input, output string
		datum rangeDatum
	}{
		{"[1.0,1.00]", "[1.0,1.00]", rangeDatum{rangeBound{coefficient: "1", scale: 1}, rangeBound{coefficient: "1", scale: 2}, rangeLowerInclusive | rangeUpperInclusive}},
		{"(4,5)", "(4,5)", rangeDatum{rangeBound{coefficient: "4"}, rangeBound{coefficient: "5"}, 0}},
		{"[1.0,1.00)", "empty", rangeDatum{flags: rangeEmpty}},
		{"[001.2300,2e1]", "[1.2300,20]", rangeDatum{rangeBound{coefficient: "123", magnitude: -2, scale: 4}, rangeBound{coefficient: "2", magnitude: 1}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[-0.000,1.230e2)", "[0.000,123.0)", rangeDatum{rangeBound{scale: 3}, rangeBound{coefficient: "123", scale: 1}, rangeLowerInclusive}},
		{"[,]", "(,)", rangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
		{"[-inf,+INF]", "[-Infinity,Infinity]", rangeDatum{rangeBound{special: numericNegativeInfinity}, rangeBound{special: numericInfinity}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[Infinity,NaN]", "[Infinity,NaN]", rangeDatum{rangeBound{special: numericInfinity}, rangeBound{special: numericNaN}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[NaN,NaN]", "[NaN,NaN]", rangeDatum{rangeBound{special: numericNaN}, rangeBound{special: numericNaN}, rangeLowerInclusive | rangeUpperInclusive}},
		{"[NaN,NaN)", "empty", rangeDatum{flags: rangeEmpty}},
		{"[NaN,)", "[NaN,)", rangeDatum{lower: rangeBound{special: numericNaN}, flags: rangeLowerInclusive | rangeUpperUnbounded}},
		{"[,-Infinity]", "(,-Infinity]", rangeDatum{upper: rangeBound{special: numericNegativeInfinity}, flags: rangeLowerUnbounded | rangeUpperInclusive}},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || !v.validRange() || v.rangeDatum != tc.datum { t.Fatalf("%q got=%#v want=%#v err=%v", tc.input, v.rangeDatum, tc.datum, err) }
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output { t.Fatal(out, err) }
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
		canonical, err := d.Input(ctx, tc.output, -1)
		if err != nil || !SameDatum(v, canonical) { t.Fatal("canonical identity", err) }
	}
	// Format limits belong to unconstrained NUMERIC, not int64 endpoints.
	for _, input := range []string{"[1e131071,)", "[,1e-16383]", "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]"} {
		v, err := d.Input(ctx, input, -1)
		if err != nil || !v.validRange() { t.Fatal(input, err) }
		out, err := d.Output(ctx, v)
		if err != nil { t.Fatal(err) }
		again, err := d.Input(ctx, out, -1)
		if err != nil || !SameDatum(v, again) { t.Fatal("format boundary identity", err) }
	}
}

func TestNumericRangeOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	d, foreign := testTypes()[NumRange], testTypes()[NumRange]
	v, err := d.Input(ctx, "[1.0,2]", -1)
	if err != nil { t.Fatal(err) }
	if _, err := foreign.Output(ctx, v); err == nil { t.Fatal("foreign output admitted") }
	if _, err := foreign.SQLLiteral(ctx, v); err == nil { t.Fatal("foreign literal admitted") }
	retag, err := v.Retag(foreign)
	if err != nil || retag.rangeDatum != v.rangeDatum || retag.Type() != foreign { t.Fatal("retag changed representation", err) }
	if _, err := v.Retag(testTypes()[Numeric]); err == nil { t.Fatal("wrong identity retag") }
	scaled, err := d.Input(ctx, "[1.00,2]", -1)
	if err != nil || SameDatum(v, scaled) { t.Fatal("display scale lost", err) }
	for _, bad := range []rangeDatum{
		{flags: 128},
		{lower: rangeBound{scale: 1}, flags: rangeEmpty},
		{lower: rangeBound{scale: 1}, flags: rangeLowerUnbounded | rangeUpperUnbounded},
		{flags: rangeLowerUnbounded | rangeLowerInclusive | rangeUpperUnbounded},
		{flags: rangeLowerUnbounded | rangeUpperUnbounded | rangeUpperInclusive},
		{rangeBound{coefficient: "2"}, rangeBound{coefficient: "1"}, rangeLowerInclusive},
		{rangeBound{coefficient: "1"}, rangeBound{coefficient: "1"}, rangeLowerInclusive},
		{lower: rangeBound{coefficient: "01"}, flags: rangeUpperUnbounded},
		{lower: rangeBound{coefficient: "10"}, flags: rangeUpperUnbounded},
		{lower: rangeBound{coefficient: "x"}, flags: rangeUpperUnbounded},
		{lower: rangeBound{negative: true}, flags: rangeUpperUnbounded},
		{lower: rangeBound{special: 4}, flags: rangeUpperUnbounded},
		{lower: rangeBound{special: numericNaN, scale: 1}, flags: rangeUpperUnbounded},
		{lower: rangeBound{coefficient: "1", magnitude: -2, scale: 1}, flags: rangeUpperUnbounded},
		{lower: rangeBound{coefficient: "1", magnitude: math.MaxInt64}, flags: rangeUpperUnbounded},
		{lower: rangeBound{scale: 16384}, flags: rangeUpperUnbounded},
	} {
		invalid := Value{typ: d, rangeDatum: bad}
		if _, err := d.Output(ctx, invalid); err == nil || SameDatum(invalid, invalid) { t.Fatal("invalid datum accepted", bad, err) }
		if _, err := invalid.Retag(foreign); err == nil { t.Fatal("invalid retag accepted", bad) }
	}
	empty, err := d.Input(ctx, "empty", -1)
	if err != nil { t.Fatal(err) }
	unbounded, err := d.Input(ctx, "(,)", -1)
	if err != nil || SameDatum(empty, unbounded) || SameDatum(Null(d), empty) { t.Fatal("NULL/empty/unbounded conflated", err) }
	if _, err := d.Output(ctx, Null(d)); err == nil { t.Fatal("NULL output") }
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" { t.Fatal(literal, err) }
	if _, err := d.Input(ctx, "[1,4]", 0); err == nil { t.Fatal("modifier accepted") }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, call := range []func() error{
		func() error { _, err := d.Input(canceled, "[1,4]", -1); return err },
		func() error { _, err := d.Output(canceled, v); return err },
		func() error { _, err := d.SQLLiteral(canceled, v); return err },
	} { if !errors.Is(call(), context.Canceled) { t.Fatal("cancellation lost") } }
	for _, input := range []string{"[1,4]\x00", "[\xff,4)"} {
		if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid character input admitted") }
	}
}

func TestNumericRangeDiagnostics(t *testing.T) {
	d := testTypes()[NumRange]
	for _, tc := range []struct{ input, code, message, detail string }{
		{"[oops,4)junk", "22P02", `malformed range literal: "[oops,4)junk"`, "Junk after right parenthesis or bracket."},
		{"[oops,1e131072]", "22P02", `invalid input syntax for type numeric: "oops"`, ""},
		{"[+NaN,4)", "22P02", `invalid input syntax for type numeric: "+NaN"`, ""},
		{"[1e131072,oops]", "22003", "value overflows numeric format", ""},
		{"[2,1e-16384)", "22003", "value overflows numeric format", ""},
		{"[Infinity,-Infinity]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		{"[NaN,Infinity]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
		{"[9007199254740992.0000000000000002,9007199254740992.0000000000000001]", "22000", "range lower bound must be less than or equal to range upper bound", ""},
	} {
		_, err := d.Input(context.Background(), tc.input, -1)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want { t.Fatalf("%q got=%#v want=%#v err=%v", tc.input, got, want, err) }
	}
}
