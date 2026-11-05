package xml

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// Report16 independently captures the NaN/Infinity/negative-value, 00/01,
// quote, malformed-mask and negative-subpattern outcomes on PostgreSQL 18.6.
// Additional sign/negative-zero boundaries follow libxslt 1.1.35 numbers.c's
// InsertNumbers, PreSuffix and FormatNumberConversion, not SQL diagnostics.
func TestXSLTNumberFormatRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, format, want string
		value              float64
	}{
		{"NaN", "1", "NaN", math.NaN()},
		{"positive infinity", "1", "Infinity", math.Inf(1)},
		{"negative infinity recovers to zero", "1", "0", math.Inf(-1)},
		{"negative recovers to zero", "1", "0", -2},
		{"negative zero", "1", "0", math.Copysign(0, -1)},
		{"zero token without one", "00", "7", 7},
		{"zero token with one", "01", "07", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatXSLTNumber(context.Background(), []float64{tc.value}, tc.format, "", "", "", "")
			if err != nil || got != tc.want {
				t.Fatalf("number %v format %q: %q, %v; want %q", tc.value, tc.format, got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, mask, want string
		value            float64
	}{
		{"quote escapes one scalar", "'ab'0", "ab012", 12},
		{"malformed mask default", "0#", "1.25", 1.25},
		{"negative subpattern without sign", "0;0", "12", -12},
		{"unsigned negative zero", "0;(0)", "0", math.Copysign(0, -1)},
		{"NaN bypasses picture", "'", "NaN", math.NaN()},
		{"infinity bypasses picture", "'", "Infinity", math.Inf(1)},
		{"negative infinity bypasses picture", "0;(0)", "-Infinity", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatXSLTDecimal(context.Background(), tc.value, tc.mask, xsltDefaultDecimalFormat())
			if err != nil || got != tc.want {
				t.Fatalf("number %v mask %q: %q, %v; want %q", tc.value, tc.mask, got, err, tc.want)
			}
		})
	}
}

func TestXSLTNumberFormatZeroDigitByte(t *testing.T) {
	symbols := xsltDefaultDecimalFormat()
	symbols.ZeroDigit = '٠'
	// Mask recognition uses the scalar U+0660, but the numeric helper receives
	// zeroDigit[0] = 0xD9. Thus 1, 2, 5 become U+00DA, U+00DB, U+00DE.
	// The leading-zero special case, unlike that helper, uses the full scalar.
	for _, tc := range []struct {
		value      float64
		mask, want string
	}{
		{12.5, "٠.٠", "ÚÛ.Þ"},
		{0.25, "٠.٠٠", "Ù.ÛÞ"},
		{0, "#", "٠"},
	} {
		got, err := formatXSLTDecimal(context.Background(), tc.value, tc.mask, symbols)
		if err != nil || got != tc.want {
			t.Fatalf("number %v mask %q: %q, %v; want %q", tc.value, tc.mask, got, err, tc.want)
		}
	}
}

func TestXSLTNumberFormatArithmeticOverflow(t *testing.T) {
	// FormatNumberConversion multiplies before rounding. Overflow is retained:
	// the integer helper sees +Inf, and the fraction helper sees Inf-Inf = NaN.
	// The pinned amd64 conversion produces INT_MIN; adding ASCII zero and
	// narrowing to a byte gives '0'. Neither helper satisfies fabs(n) < 1,
	// so each fills its backward 500-byte buffer except the NUL terminator.
	zeros := strings.Repeat("0", 499)
	for _, tc := range []struct {
		name, mask, prefix, suffix string
		value                      float64
	}{
		{"observed scale product", "0.00", "", "", 1e308},
		{"different scale product", "0.0", "", "", 1e308},
		{"negative scale product", "0.00", "-", "", -1e308},
		{"multiplier then scale", "0.00%", "", "%", 1e306},
		{"scale itself overflows", "0." + strings.Repeat("0", 309), "", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.prefix + zeros + "." + zeros + tc.suffix
			got, err := formatXSLTDecimal(context.Background(), tc.value, tc.mask, xsltDefaultDecimalFormat())
			if err != nil || got != want {
				t.Fatalf("number %v mask %q: %q, %v; want %q", tc.value, tc.mask, got, err, want)
			}
		})
	}
}

func TestXSLTNumberFormatBackwardBuffer(t *testing.T) {
	// Independent helper boundaries distinguish finite width exhaustion from
	// nonfinite arithmetic, and byte capacity from a count of Unicode scalars.
	for _, tc := range []struct {
		name  string
		value float64
		zero  rune
		width int
		want  string
	}{
		{"exact payload", 0, '0', 499, strings.Repeat("0", 499)},
		{"width exceeds payload", 0, '0', 500, strings.Repeat("0", 499)},
		{"multibyte payload", 0, '٠', 500, strings.Repeat("٠", 249)},
		{"infinite integer", math.Inf(1), '0', 1, strings.Repeat("0", 499)},
		{"NaN fraction", math.NaN(), '0', 0, strings.Repeat("0", 499)},
		{"nonfinite conversion adds zero byte", math.NaN(), '1', 0, strings.Repeat("1", 499)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parser{ctx: context.Background()}
			var out strings.Builder
			xsltNumberDecimal(&p, &out, tc.value, tc.zero, tc.width, 0, 0)
			if p.err != nil || out.String() != tc.want {
				t.Fatalf("got %q, %v; want %q", out.String(), p.err, tc.want)
			}
		})
	}
}

func TestXSLTNumberFormatOverflowCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		ctx := &phaseContext{Context: context.Background(), phase: "xsltNumberDecimal", after: 3, cause: cause}
		got, err := formatXSLTDecimal(ctx, 1e308, "0.00", xsltDefaultDecimalFormat())
		if !ctx.admitted || got != "" || !errors.Is(err, cause) {
			t.Fatalf("overflow cancellation admitted %t result %q error %v", ctx.admitted, got, err)
		}
		if _, ok := err.(interface{ SQLState() string }); ok {
			t.Fatal("ordinary cancellation acquired SQLSTATE")
		}
	}
	got, err := formatXSLTDecimal(context.Background(), 12.5, "0.0", xsltDefaultDecimalFormat())
	if err != nil || got != "12.5" {
		t.Fatalf("healthy call after canceled overflow: %q, %v", got, err)
	}
}
