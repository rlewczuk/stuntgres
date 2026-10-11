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
				datum         integerRangeDatum
			}{
				{"[1,4]", "[1,5)", integerRangeDatum{1, 5, rangeLowerInclusive}},
				{"(1,4)", "[2,4)", integerRangeDatum{2, 4, rangeLowerInclusive}},
				{"(1,4]", "[2,5)", integerRangeDatum{2, 5, rangeLowerInclusive}},
				{"[4,4]", "[4,5)", integerRangeDatum{4, 5, rangeLowerInclusive}},
				{"[4,4)", "empty", integerRangeDatum{flags: rangeEmpty}},
				{"(4,4]", "empty", integerRangeDatum{flags: rangeEmpty}},
				{"(4,4)", "empty", integerRangeDatum{flags: rangeEmpty}},
				{"(4,5)", "empty", integerRangeDatum{flags: rangeEmpty}},
				{" \tEMPTY\r\n", "empty", integerRangeDatum{flags: rangeEmpty}},
				{"[,]", "(,)", integerRangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"(,]", "(,)", integerRangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"[,)", "(,)", integerRangeDatum{flags: rangeLowerUnbounded | rangeUpperUnbounded}},
				{"[,4]", "(,5)", integerRangeDatum{0, 5, rangeLowerUnbounded}},
				{"[1,]", "[1,)", integerRangeDatum{1, 0, rangeLowerInclusive | rangeUpperUnbounded}},
				{" [ +0001 , 0x_F ] ", "[1,16)", integerRangeDatum{1, 16, rangeLowerInclusive}},
				{`["1","4"]`, "[1,5)", integerRangeDatum{1, 5, rangeLowerInclusive}},
				{`["1"2,14)`, "[12,14)", integerRangeDatum{12, 14, rangeLowerInclusive}},
				{`[\1,4]`, "[1,5)", integerRangeDatum{1, 5, rangeLowerInclusive}},
			} {
				v, err := d.Input(ctx, tc.input, -1)
				if err != nil || v.IsNull() || !v.validIntegerRange() || v.rangeDatum != tc.datum {
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
				datum integerRangeDatum
			}{
				{fmt.Sprintf("[%d,%d)", minimum, maximum), integerRangeDatum{minimum, maximum, rangeLowerInclusive}},
				{fmt.Sprintf("(%d,%d)", minimum, maximum), integerRangeDatum{minimum + 1, maximum, rangeLowerInclusive}},
				{fmt.Sprintf("[%d,)", maximum), integerRangeDatum{maximum, 0, rangeLowerInclusive | rangeUpperUnbounded}},
				{fmt.Sprintf("(,%d)", minimum), integerRangeDatum{0, minimum, rangeLowerUnbounded}},
				{fmt.Sprintf("[%d,%d)", maximum, maximum), integerRangeDatum{flags: rangeEmpty}},
				{fmt.Sprintf("(%d,%d]", maximum, maximum), integerRangeDatum{flags: rangeEmpty}},
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
		for _, bad := range []integerRangeDatum{{1, 5, 0}, {1, 5, rangeUpperInclusive}, {1, 0, rangeLowerUnbounded}, {0, 1, rangeUpperUnbounded}, {1, 0, rangeEmpty}, {5, 1, rangeLowerInclusive}, {1, 1, rangeLowerInclusive}, {1, 2, 128}} {
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
