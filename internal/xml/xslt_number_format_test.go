package xml

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestXSLTNumberFormatSequence(t *testing.T) {
	for _, tc := range []struct {
		name, format, separator, size, letter, lang string
		values                                      []float64
		want                                        string
	}{
		{name: "decimal", format: "01", values: []float64{1, 2, 3}, want: "01.02.03"},
		{name: "tokens", format: "(01-A:i)", values: []float64{3, 27, 9, 10}, want: "(03-AA:ix:x)"},
		{name: "one token repeats default separator", format: "(1)", values: []float64{1, 2}, want: "(1.2)"},
		{name: "punctuation only", format: "--", values: []float64{1, 2}, want: "--1.2"},
		{name: "empty format", values: []float64{1, 2}, want: "1.2"},
		{name: "empty sequence", format: "(1)", want: ""},
		{name: "all zero token recovers", format: "000", values: []float64{3}, want: "3"},
		{name: "numeric token prefix", format: "0001suffix", values: []float64{12}, want: "0012"},
		{name: "unknown alphabet", format: "α", values: []float64{27}, want: "27"},
		{name: "recognized alphabet prefix", format: "ABC", values: []float64{27}, want: "AA"},
		{name: "lower alphabet", format: "a", values: []float64{1, 26, 27, 52, 53, 702, 703}, want: "a.z.aa.az.ba.zz.aaa"},
		{name: "alphabet lower bound", format: "A", values: []float64{0}, want: "0"},
		{name: "Roman boundaries", format: "I", values: []float64{0, 1, 4, 9, 49, 944, 3999, 5000, 5001}, want: "0.I.IV.IX.XLIX.CMXLIV.MMMCMXCIX.MMMMM.5001"},
		{name: "Roman ignores letter value", format: "i", letter: "alphabetic", lang: "ja", values: []float64{4}, want: "iv"},
		{name: "half rounds up", format: "1", values: []float64{0.49, 0.5, 1.5, 2.5, -0.5, -2}, want: "0.1.2.3.0.0"},
		{name: "nonfinite", format: "01", values: []float64{math.NaN(), math.Inf(1), math.Inf(-1)}, want: "NaN.Infinity.00"},
		{name: "grouping", format: "0000001", separator: " ", size: "3", values: []float64{12345}, want: "0 012 345"},
		{name: "separator first scalar", format: "1", separator: "🙂ignored", size: "2", values: []float64{12345}, want: "1🙂23🙂45"},
		{name: "integer group syntax", format: "1", separator: ",", size: " +3tail", values: []float64{12345}, want: "12,345"},
		{name: "zero group disabled", format: "1", separator: ",", size: "0", values: []float64{12345}, want: "12345"},
		{name: "negative group disabled", format: "1", separator: ",", size: "-2", values: []float64{12345}, want: "12345"},
		{name: "noninteger group disabled", format: "1", separator: ",", size: "text", values: []float64{12345}, want: "12345"},
		{name: "grouping absent", format: "1", size: "3", values: []float64{12345}, want: "12345"},
		{name: "new Unicode digit is punctuation", format: "𝟙", values: []float64{7}, want: "𝟙7"},
		{name: "combining is separator", format: "Á1", values: []float64{1, 2}, want: "Á2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatXSLTNumber(context.Background(), tc.values, tc.format, tc.separator, tc.size, tc.letter, tc.lang)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestXSLTNumberFormatDigits(t *testing.T) {
	// These are the fourteen explicit xsltIsDigitZero ranges, not host Unicode
	// Nd. Tamil digits, fullwidth digits and modern mathematical digits are not
	// supported decimal tokens by the released helper.
	for _, tc := range []struct{ format, want string }{
		{"001", "012"}, {"٠٠١", "٠١٢"}, {"۰۰۱", "۰۱۲"}, {"००१", "०१२"},
		{"০০১", "০১২"}, {"੦੦੧", "੦੧੨"}, {"૦૦૧", "૦૧૨"}, {"୦୦୧", "୦୧୨"},
		{"౦౦౧", "౦౧౨"}, {"೦೦೧", "೦೧೨"}, {"൦൦൧", "൦൧൨"}, {"๐๐๑", "๐๑๒"},
		{"໐໐໑", "໐໑໒"}, {"༠༠༡", "༠༡༢"}, {"௧", "12"}, {"１", "１12"},
	} {
		got, err := formatXSLTNumber(context.Background(), []float64{12}, tc.format, "", "", "", "")
		if err != nil || got != tc.want {
			t.Fatalf("format %q: %q %v, want %q", tc.format, got, err, tc.want)
		}
	}
}

func TestXSLTNumberFormatDecimalPictures(t *testing.T) {
	for _, tc := range []struct {
		number     float64
		mask, want string
	}{
		{1234.5, "#,##0.00", "1,234.50"},
		{12345678, "#,##,##0", "12,345,678"},
		{12.345, "0.00", "12.35"},
		{2.5, "0", "3"},
		{-2.5, "0", "-3"},
		{9.999, "0.00", "10.00"},
		{0, "#", "0"},
		{0, "#.##", "0"},
		{0, ".##", ".0"},
		{0.5, ".##", ".5"},
		{0.5, "#.##", "0.5"},
		{0.5, "#.00", ".50"},
		{12, "000.###", "012"},
		{12, "0.", "12."},
		{0.125, "0%", "13%"},
		{0.125, "%0.0", "%12.5"},
		{0.0125, "0.0‰", "12.5‰"},
		{12, "pre0post", "pre12post"},
		{-12, "pre0post", "-pre12post"},
		{-12, "0;(0)", "(12)"},
		{-12.5, "0.00;(0.0)", "(12.50)"},
		{-12, "0;0", "12"},
		{12, "0;garbage", "12"},
		{math.Copysign(0, -1), "0;(0)", "0"},
		{12, "'ab'0", "ab012"},
		{12, "''0", "'12"},
		{12, "'0", "012"},
		{12, "0'%'", "12.0"},
		{1.25, "0#", "1.25"},
		{12, "0.0#0", "12.0"},
		{12, "0..0", "12.0"},
		{12, "0%%", "12.0"},
		{12, "0;0;0", "12"},
		{-12, "0;0;0", "12"},
		{-12, "0;0x0", "-12.0"},
		{12, "", "12"},
		{0, "", "0"},
		{12, "x", "x12"},
	} {
		got, err := formatXSLTDecimal(context.Background(), tc.number, tc.mask, xsltDefaultDecimalFormat())
		if err != nil || got != tc.want {
			t.Fatalf("number %v picture %q: %q %v, want %q", tc.number, tc.mask, got, err, tc.want)
		}
	}
}

func TestXSLTNumberFormatDecimalSymbols(t *testing.T) {
	symbols := xsltDefaultDecimalFormat()
	symbols.DecimalSeparator = ','
	symbols.GroupingSeparator = '.'
	symbols.MinusSign = '−'
	symbols.Percent = '٪'
	symbols.PerMille = '؉'
	symbols.PatternSeparator = '|'
	symbols.Digit = '?'
	symbols.Infinity = "∞"
	symbols.NaN = "not a number"
	for _, tc := range []struct {
		number     float64
		mask, want string
	}{
		{1234.5, "?.??0,00", "1.234,50"},
		{-1234.5, "?.??0,00", "−1.234,50"},
		{-1234.5, "?.??0,00|(0)", "(1.234,50)"},
		{0.125, "0,0٪", "12,5٪"},
		{0.0125, "0,0؉", "12,5؉"},
		{math.Inf(1), "bad'", "∞"},
		{math.Inf(-1), "(0)", "−∞"},
		{math.NaN(), "prefix0suffix", "not a number"},
	} {
		got, err := formatXSLTDecimal(context.Background(), tc.number, tc.mask, symbols)
		if err != nil || got != tc.want {
			t.Fatalf("number %v picture %q: %q %v, want %q", tc.number, tc.mask, got, err, tc.want)
		}
	}
}

func TestXSLTNumberFormatBoundaries(t *testing.T) {
	// Release helper's backward buffer is 500 bytes including its terminator.
	for _, tc := range []struct{ format, separator, size, want string }{
		{strings.Repeat("0", 600) + "1", "", "", strings.Repeat("0", 498) + "1"},
		{strings.Repeat("0", 600) + "1", ",", "1", strings.Repeat("0,", 249) + "1"},
		{strings.Repeat("1.", 1024) + "A", "", "", "1.2."},
	} {
		values := []float64{1}
		if strings.HasSuffix(tc.format, "A") {
			values = []float64{1, 2}
		}
		got, err := formatXSLTNumber(context.Background(), values, tc.format, tc.separator, tc.size, "", "")
		if err != nil || got != tc.want {
			t.Fatalf("boundary: %q %v want %q", got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		r    rune
		want bool
	}{
		{'A', true}, {'z', true}, {'0', true}, {'é', true}, {'α', true}, {'中', true},
		{'\u3007', true}, {'\u3029', true}, {'\u9fa5', true}, {'\u9fa6', false},
		{'\u0132', false}, {'\u0301', false}, {'\u200c', false}, {'𝟙', false}, {'🙂', false},
	} {
		if got := xsltNumberAlphanumeric(tc.r); got != tc.want {
			t.Fatalf("classification U+%04X: %t", tc.r, got)
		}
	}
}

func TestXSLTNumberFormatCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, tc := range []struct {
			phase       string
			decimal     bool
			mask, group string
		}{
			{phase: "formatXSLTNumber", mask: strings.Repeat("-", 10000) + "1"},
			{phase: "formatXSLTNumber", mask: strings.Repeat("0", 10000) + "1"},
			{phase: "xsltNumberGroupingSize", mask: "1", group: strings.Repeat(" ", 10000) + "3"},
			{phase: "xsltNumberDecimal", mask: strings.Repeat("0", 600) + "1"},
			{phase: "affix", decimal: true, mask: strings.Repeat("x", 10000) + "0"},
			{phase: "picture", decimal: true, mask: strings.Repeat("#", 10000) + "0"},
			{phase: "emitAffix", decimal: true, mask: strings.Repeat("x", 10000) + "0"},
			{phase: "xsltNumberDecimal", decimal: true, mask: strings.Repeat("0", 600)},
		} {
			ctx := &phaseContext{Context: context.Background(), phase: tc.phase, after: 3, cause: cause}
			var result string
			var err error
			if tc.decimal {
				result, err = formatXSLTDecimal(ctx, 12.5, tc.mask, xsltDefaultDecimalFormat())
			} else {
				result, err = formatXSLTNumber(ctx, []float64{12}, tc.mask, ",", tc.group, "", "")
			}
			if !ctx.admitted || !errors.Is(err, cause) || result != "" {
				t.Fatalf("phase %s: admitted %t result %q error %v", tc.phase, ctx.admitted, result, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary cancellation acquired SQLSTATE")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := formatXSLTNumber(ctx, nil, "", "", "", "", ""); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	if got, err := formatXSLTDecimal(ctx, math.NaN(), "", xsltDefaultDecimalFormat()); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
}

func TestXSLTNumberFormatOwnership(t *testing.T) {
	var workers sync.WaitGroup
	failures := make(chan string, 8)
	for i := range 8 {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			symbols := xsltDefaultDecimalFormat()
			want := "1,234.50"
			if i%2 == 1 {
				symbols.GroupingSeparator = '_'
				want = "1_234.50"
			}
			mask := "#" + string(symbols.GroupingSeparator) + "##0.00"
			for range 16 {
				got, err := formatXSLTDecimal(context.Background(), 1234.5, mask, symbols)
				if err != nil || got != want {
					failures <- "decimal symbols leaked across invocations"
					return
				}
				sequence, err := formatXSLTNumber(context.Background(), []float64{1, 2}, "(01)", "", "", "", "")
				if err != nil || sequence != "(01.02)" || got != want {
					failures <- "later invocation changed retained result"
					return
				}
			}
		}(i)
	}
	workers.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

func TestXSLTNumberFormatPublication(t *testing.T) {
	for _, decimal := range []bool{false, true} {
		ctx := &phaseContext{Context: context.Background(), phase: "xsltNumberDecimal", final: true, cause: context.Canceled}
		var got string
		var err error
		if decimal {
			got, err = formatXSLTDecimal(ctx, 12.5, "0.0", xsltDefaultDecimalFormat())
		} else {
			got, err = formatXSLTNumber(ctx, []float64{12}, "1", "", "", "", "")
		}
		if !ctx.admitted || got != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("publication decimal %t: %q %v", decimal, got, err)
		}
	}
	if got, err := formatXSLTDecimal(context.Background(), 12.5, "0.0", xsltDefaultDecimalFormat()); err != nil || got != "12.5" {
		t.Fatal(got, err)
	}
	if got, err := formatXSLTNumber(context.Background(), []float64{12}, "1", "", "", "", ""); err != nil || got != "12" {
		t.Fatal(got, err)
	}
	// A resource failure from the shared checked builder is sticky; numeric
	// polling must not erase it merely because the context remains healthy.
	resource := appendSizeError(0x3ffffffe, 1)
	p := parser{ctx: context.Background(), err: resource}
	var out strings.Builder
	xsltNumberDecimal(&p, &out, 12, '0', 1, 0, 0)
	if p.err != resource || out.Len() != 0 {
		t.Fatal("resource failure overwritten", p.err, out.String())
	}
}
