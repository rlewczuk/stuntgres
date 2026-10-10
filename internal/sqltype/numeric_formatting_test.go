package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestNumericFormattingOutput(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct {
		id                 ID
		source, mask, want string
	}{
		{Numeric, "-0.1", "99.99", "  -.10"}, {Numeric, "-0.1", "FM9.99", "-.1"}, {Numeric, "-0.1", "FM90.99", "-0.1"},
		{Int4, "12", "9990999.9", "    0012.0"}, {Int4, "12", "FM9990999.9", "0012."}, {Numeric, "148.5", "FM999.990", "148.500"},
		{Numeric, "100", "FM999.9", "100."}, {Numeric, "100", "FM999.", "100"}, {Numeric, "2.5", "FM9", "3"}, {Numeric, "-2.5", "FM9", "-3"},
		{Int8, "9007199254740993", "FM9999999999999999", "9007199254740993"}, {Int8, "-9223372036854775808", "9.999999999999999999EEEE", "-9.223372036854775808e+18"},
		{Int4, "999", "9EEEE", " 1e+03"}, {Int8, "999", "9EEEE", " 10e+02"}, {Numeric, "999", "9EEEE", " 10e+02"},
		{Int4, "2147483647", "FM9999999999V9", "-10"}, {Int4, "1", "FM9V999999999", "1000000000"}, {Int4, "1", "FM9V9999999999", "-2147483648"},
		{Int8, "1", "FM9V999999999999999999", "1000000000000000000"},
		{Numeric, "9.995", "FM9.99", "#.##"}, {Numeric, "9.995", "9.99EEEE", " 10.00e+00"}, {Numeric, "0", "9.99EEEE", " 0.00e+00"},
		{Numeric, "1e-16383", "9.999EEEE", " 1.000e-16383"}, {Numeric, "-1.2345e131071", "9.999EEEE", "-1.235e+131071"},
		{Float8, "2.5", "FM9", "2"}, {Float4, "2.5", "FMRN", "II"}, {Numeric, "2.5", "FMRN", "III"}, {Float8, "-0", "FM9.0", "-.0"},
		{Float4, "1", "FM9.000000000000000000", "1.00000"}, {Float8, "1", "FM9.000000000000000000", "1.00000000000000"},
		{Float8, "12345678901", "FM9999999999D9999900000000000000000", "##########.####"},
		{Float4, "Infinity", "FM9.99", "#."}, {Float8, "Infinity", "FM9.99", "#.##"}, {Numeric, "NaN", "9.999EEEE", " #.#######"},
		{Int4, "1485", "9G999", " 1,485"}, {Numeric, "148.5", "999D999", " 148.500"}, {Int4, "485", "L999", "  485"},
		{Numeric, "12.45", "99V9", " 125"}, {Int4, "12", "99FM.99", "12."}, {Int4, "12", "FMFM99.99", "12."},
		{Int4, "12", "Fm99.99", "Fm 12.00"}, {Int4, "1", "9SPL", " 1 "}, {Int4, "1", "9SPR", " 1R"},
		{Int4, "482", "999th", " 482nd"}, {Int4, "12", `FM99"é猫"`, "12é猫"}, {Int4, "12", `FM99"unfinished`, "12unfinished"},
	} {
		t.Run(fmt.Sprint(c.id)+"/"+c.mask+"/"+c.source, func(t *testing.T) {
			source, err := types[c.id].Input(ctx, c.source, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, constant := range []bool{false, true} {
				var mask *string
				if constant {
					mask = &c.mask
				}
				p, err := BindNumericFormat(ctx, "to_char", types[Text].Usage(), types[c.id].Usage(), mask)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					v, err := p.Evaluate(ctx, source, String(types[Text], c.mask), InputEnvironment{}, OutputEnvironment{})
					if err != nil {
						t.Fatal(err)
					}
					got, _ := v.Text()
					if got != c.want {
						t.Errorf("constant=%v got %q want %q", constant, got, c.want)
					}
				}
			}
		})
	}
}
func TestNumericFormattingSigns(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct {
		mask string
		want [3]string
	}{
		{"S999", [3]string{" -12", "  +0", " +12"}}, {"999S", [3]string{" 12-", "  0+", " 12+"}},
		{"MI999", [3]string{"- 12", "   0", "  12"}}, {"999MI", [3]string{" 12-", "  0 ", " 12 "}},
		{"PL999", [3]string{"  -12", "+   0", "+  12"}}, {"999PL", [3]string{" -12 ", "   0+", "  12+"}},
		{"SG999", [3]string{"- 12", "+  0", "+ 12"}}, {"999SG", [3]string{" 12-", "  0+", " 12+"}},
		{"999PR", [3]string{" <12>", "   0 ", "  12 "}}, {"FM999PR", [3]string{"<12>", "0", "12"}},
	} {
		p, err := BindNumericFormat(ctx, "to_char", types[Text].Usage(), types[Numeric].Usage(), &c.mask)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range []string{"-12", "0", "12"} {
			source, err := types[Numeric].Input(ctx, s, -1)
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Evaluate(ctx, source, String(types[Text], c.mask), InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := v.Text()
			if got != c.want[i] {
				t.Errorf("%s %s got %q want %q", c.mask, s, got, c.want[i])
			}
		}
	}
}
func TestNumericFormattingInput(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct{ source, mask, want string }{
		{"-34,338,492.654,878", "99G999G999D999G999", "-34338492.654878"}, {"<564646.654564>", "999999.999999PR", "-564646.654564"},
		{"5.01-", "FM9.999999S", "-5.01"}, {"5.01-", "FM9.999999MI", "-5.01"}, {".01", "FM9.99", "0.01"}, {".0", "99999999.99999999", "0.0"},
		{"0", "99.99", "0"}, {"34,50", "999,99", "3450"}, {"123,000", "999G", "123"}, {"123456", "999G999", "123456"},
		{"$1,234.56", "L99,999.99", "1234.56"}, {"42nd", "99th", "42"}, {"123456", "99999V99", "1234.560000000000000000"},
		{"CvIiI", "rn", "108"}, {"M CC", "RN", "1000"}, {"12", "9999", "12"}, {"123tail", "99", "12"}, {"1x2", "9x9", "12"},
		{"猫12", `"é"99`, "12"}, {"€12", "L99", "12"}, {"--1", "99", "-1"}, {"+-1", "99", "1"}, {"1-", "99", "1"}, {"1.2.3", "99.99", "1.23"},
		{"12", "9V0", "12.0000000000000000"}, {"12", "9V", "1.0000000000000000"}, {"123", "9V9V9", "1.230000000000000000"},
	} {
		p, err := BindNumericFormat(ctx, "to_number", types[Numeric].Usage(), types[Text].Usage(), &c.mask)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			v, err := p.Evaluate(ctx, String(types[Text], c.source), String(types[Text], c.mask), InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(c, err)
			}
			if got := numericOutput(v); got != c.want {
				t.Errorf("%+v got %q", c, got)
			}
		}
	}
}
func TestNumericFormattingRomanExhaustive(t *testing.T) {
	// Independent positional tables cover every canonical numeral, not a
	// roundtrip that could let matching encoder/reader defects cancel each other.
	thousands := []string{"", "M", "MM", "MMM"}
	hundreds := []string{"", "C", "CC", "CCC", "CD", "D", "DC", "DCC", "DCCC", "CM"}
	tens := []string{"", "X", "XX", "XXX", "XL", "L", "LX", "LXX", "LXXX", "XC"}
	ones := []string{"", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX"}
	for n := 1; n <= 3999; n++ {
		want := thousands[n/1000] + hundreds[n/100%10] + tens[n/10%10] + ones[n%10]
		if got := numericRoman(n); got != want {
			t.Fatal(n, got, want)
		}
		got, used, err := numericReadRoman(context.Background(), want)
		if err != nil || got != n || used != len(want) {
			t.Fatal(n, got, used, err)
		}
	}
	for _, s := range []string{"viv", "MMMM", "VV", "IL", "VIX", "qiv", " ", ""} {
		_, _, err := numericReadRoman(context.Background(), s)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22P02" || d.Message != "invalid Roman numeral" {
			t.Fatal(s, err)
		}
	}
}
func TestNumericFormattingVFactorFirst(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, n := range []int{984, 985, 1000, 1001, 1002} {
		mask := "9V" + strings.Repeat("9", n)
		p, err := BindNumericFormat(ctx, "to_number", types[Numeric].Usage(), types[Text].Usage(), &mask)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(ctx, String(types[Text], "1"), String(types[Text], mask), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		scale := min(16+n, 1000)
		want := "0." + strings.Repeat("0", scale)
		if n <= 1000 {
			want = "0." + strings.Repeat("0", n-1) + "1" + strings.Repeat("0", scale-n)
		}
		if got := numericOutput(v); got != want {
			t.Errorf("n=%d got %q want %q", n, got, want)
		}
	}
	// An enormous source must not resurrect a factor already rounded to zero.
	mask := strings.Repeat("9", 1002) + "V" + strings.Repeat("9", 1001)
	plan, err := BindNumericFormat(ctx, "to_number", types[Numeric].Usage(), types[Text].Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	v, err := plan.Evaluate(ctx, String(types[Text], "1"+strings.Repeat("0", 1001)), String(types[Text], mask), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got := numericOutput(v); got != "0."+strings.Repeat("0", 1000) {
		t.Fatal("shift-before-round revived underflowed factor", got)
	}
}
func TestNumericFormattingDeferredDiagnosticsAndOwnership(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	mask := "9.9.9"
	p, err := BindNumericFormat(ctx, "to_char", types[Text].Usage(), types[Numeric].Usage(), &mask)
	if err != nil {
		t.Fatal("constant mask failed binding", err)
	}
	source, err := types[Numeric].Input(ctx, "12", -1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Evaluate(ctx, source, String(types[Text], mask), InputEnvironment{}, OutputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42601" || d.Message != "multiple decimal points" || d.Position != "" {
		t.Fatal(err)
	}
	d.Position = "mutated"
	_, err = p.Evaluate(ctx, source, String(types[Text], mask), InputEnvironment{}, OutputEnvironment{})
	if !errors.As(err, &d) || d.Position != "" {
		t.Fatal("cached error mutation", err)
	}
	v, err := p.Evaluate(ctx, Null(types[Numeric]), String(types[Text], mask), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !v.IsNull() {
		t.Fatal(v, err)
	}
	next := mathTypes()
	rebound, err := p.Retag(next[Text].Usage(), next[Numeric].Usage())
	if err != nil {
		t.Fatal(err)
	}
	v, err = rebound.Evaluate(ctx, Null(next[Numeric]), String(next[Text], mask), InputEnvironment{}, OutputEnvironment{})
	if err != nil || v.Type() != next[Text] || p.result.Type != types[Text] {
		t.Fatal("retag ownership", v, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := BindNumericFormat(canceled, "to_char", types[Text].Usage(), types[Numeric].Usage(), &mask); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Evaluate(canceled, source, String(types[Text], mask), InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestNumericFormattingTemplateBounds(t *testing.T) {
	for _, name := range []string{"to_char", "to_number"} {
		limit := int(((int64(1) << 31) - 1) / 8)
		if name == "to_char" {
			limit = int(((int64(1) << 31) - 1 - 4) / 8)
		}
		if !numericFormatEmpty(name, 0) || numericFormatEmpty(name, limit-1) || !numericFormatEmpty(name, limit) {
			t.Fatal(name, limit)
		}
	}
}

func TestNumericFormattingDescriptorDiagnostics(t *testing.T) {
	cases := []struct{ mask, message, detail string }{
		{"9.9.9", "multiple decimal points", ""}, {"9D9.9", "multiple decimal points", ""},
		{"9V9.9", `cannot use "V" and decimal point together`, ""}, {"9.9V9", `cannot use "V" and decimal point together`, ""},
		{"9SS", `cannot use "S" twice`, ""}, {"9MIS", `cannot use "S" and "PL"/"MI"/"SG"/"PR" together`, ""},
		{"9SMI", `cannot use "S" and "MI" together`, ""}, {"9SSG", `cannot use "S" and "SG" together`, ""},
		{"9PLPR", `cannot use "PR" and "S"/"PL"/"MI"/"SG" together`, ""},
		{"PR9", `"9" must be ahead of "PR"`, ""}, {"PR0", `"0" must be ahead of "PR"`, ""},
		{"RNRN", `cannot use "RN" twice`, ""},
		{"RNMI", `"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`},
		{"9EEEEEEEE", `cannot use "EEEE" twice`, ""}, {"9EEEE9.9.9", `"EEEE" must be the last pattern used`, ""},
		{"9.9.9EEEE9", "multiple decimal points", ""},
		{"FM9EEEE", `"EEEE" is incompatible with other formats`, `"EEEE" may only be used together with digit and decimal point patterns.`},
	}
	for _, c := range cases {
		for _, mask := range []string{c.mask, strings.ToLower(c.mask)} {
			_, err := compileNumericFormat(context.Background(), mask)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42601" || d.Message != c.message || d.Detail != c.detail || d.Position != "" {
				t.Fatal(mask, err)
			}
		}
	}
	for _, mask := range []string{"RN9", "9RN", "RNG", "9GEEEE", `9EEEE"tail"`, "9V0", "9V9V9", "9V", "V9", "9V00", "9SPL", "9SPR", "9spl", "9spr"} {
		if _, err := compileNumericFormat(context.Background(), mask); err != nil {
			t.Fatal("overstrict descriptor", mask, err)
		}
	}
}
func TestNumericFormattingPrivateInputSizing(t *testing.T) {
	// Release-source-derived private typmod boundaries, not public declaration
	// precision validation. These supplement the pinned long-V public vectors.
	if p, s := numericParts(numericFormatTypmod(1001, 0)); p != 1001 || s != 0 {
		t.Fatal(p, s)
	}
	if typmod := numericFormatTypmod(32768, 0); typmod != -1 {
		t.Fatal(typmod)
	}
	if p, s := numericParts(numericFormatTypmod(1, 1024)); p != 1 || s != -1024 {
		t.Fatal(p, s)
	}
	types := mathTypes()
	v, err := types[Numeric].numericInput("1."+strings.Repeat("0", 1024), numericFormatTypmod(1, 1024))
	if err != nil || numericOutput(v) != "0" {
		t.Fatal(v, err)
	}
}
func TestNumericFormattingIntegerWrapperErrors(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, c := range []struct{ source, mask string }{
		{"9223372036854775807", "FM9999999999999999999V9"},
		{"1", "FM9V9999999999999999999"},
	} {
		v, err := types[Int8].Input(ctx, c.source, -1)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BindNumericFormat(ctx, "to_char", types[Text].Usage(), types[Int8].Usage(), &c.mask)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plan.Evaluate(ctx, v, String(types[Text], c.mask), InputEnvironment{}, OutputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "bigint out of range" || d.Position != "" {
			t.Fatal(err)
		}
	}
}
