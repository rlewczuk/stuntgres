package sqltype

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestNumericCodecAndBounds(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Numeric]
	if d.ID() != 1700 || d.Name() != "numeric" || d.DatabaseName() != "NUMERIC" {
		t.Fatal(d)
	}
	for _, tc := range []struct{ in, out string }{{"001.2300", "1.2300"}, {"-0.000", "0.000"}, {".1", "0.1"}, {"1.230e2", "123.0"}, {"1.230e-2", "0.01230"}, {"5e2", "500"}, {"0x8000000000000000", "9223372036854775808"}, {"0o1000000000000000000000", "9223372036854775808"}, {"0b1" + strings.Repeat("0", 63), "9223372036854775808"}, {"inf", "Infinity"}, {"-INF", "-Infinity"}, {"nan", "NaN"}, {"1.618_034", "1.618034"}} {
		v, err := d.Input(ctx, tc.in, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.out {
			t.Fatal(tc, out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.out+"'" {
			t.Fatal(literal, err)
		}
	}
	for _, in := range []string{"oops", "+NaN", "-NaN", "Infinityx", "1__2", "1._2", "1e_2", "0x__1", "1e+"} {
		_, err := d.Input(ctx, in, -1)
		numericProviderError(t, err, "22P02", "invalid input syntax for type numeric: \""+in+"\"")
	}
	for _, in := range []string{"1e131072", "1e-16384", "9e-16384", "1e-20000", "1e999999999999999999999"} {
		_, err := d.Input(ctx, in, -1)
		numericProviderError(t, err, "22003", "value overflows numeric format")
	}
	for _, tc := range []struct {
		in     string
		length int
		last   byte
	}{{"1e131071", 131072, '0'}, {"1e-16383", 16385, '1'}} {
		v, err := d.Input(ctx, tc.in, -1)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := d.Output(ctx, v)
		if len(out) != tc.length || out[len(out)-1] != tc.last {
			t.Fatal(len(out))
		}
	}
}
func numericProviderError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Code != code || got.Message != message || got.Position != "" {
		t.Fatal(got, err, code, message)
	}
}
func TestNumericTypmodsAndImmutableSizing(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Numeric]
	for _, tc := range []struct {
		p, s    int32
		in, out string
	}{{3, 1, "1.25", "1.3"}, {3, 1, "-1.25", "-1.3"}, {3, 1, "99.94", "99.9"}, {2, 0, "2.5", "3"}, {2, 0, "-2.5", "-3"}, {2, -3, "1499", "1000"}, {2, -3, "1500", "2000"}, {2, -3, "-1500", "-2000"}, {2, -3, "99499", "99000"}, {3, 5, ".009994", "0.00999"}, {3, 5, ".000004", "0.00000"}, {1, 1000, "1e-1001", "0." + strings.Repeat("0", 1000)}, {1000, -1000, "1e1999", "1" + strings.Repeat("0", 1999)}} {
		u, w, err := d.ResolveModifiers(tc.p, tc.s)
		if err != nil || w != nil || !u.Valid() {
			t.Fatal(u, w, err)
		}
		p, s, ok := u.DecimalSize()
		if !ok || p != int64(tc.p) || s != int64(tc.s) {
			t.Fatal(p, s, ok)
		}
		v, err := d.Input(ctx, tc.in, -1)
		if err != nil {
			t.Fatal(err)
		}
		original := v
		plan, err := PlanCoercion(d.Usage(), u, Assignment)
		if err != nil {
			t.Fatal(err)
		}
		sized, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		out, _ := d.Output(ctx, sized)
		if out != tc.out || v != original {
			t.Fatal(tc, out)
		}
	}
	for _, tc := range []struct {
		p, s int32
		in   string
	}{{3, 1, "99.95"}, {3, 1, "-99.95"}, {2, -3, "99500"}, {3, 5, ".009995"}, {2, 2, "1.00"}, {3, 1, "Infinity"}} {
		u, _, _ := d.ResolveModifiers(tc.p, tc.s)
		_, err := d.Input(ctx, tc.in, u.Typmod)
		numericProviderError(t, err, "22003", "numeric field overflow")
	}
	for _, mods := range [][]int32{{0}, {1001}, {2, 1001}, {2, -1001}, {2, 1, 0}} {
		_, _, err := d.ResolveModifiers(mods...)
		if err == nil {
			t.Fatal(mods)
		}
	}
	for _, mods := range [][]int32{{1, -1000}, {1000, 1000}} {
		u, _, err := d.ResolveModifiers(mods...)
		if err != nil || !u.Valid() {
			t.Fatal(u, err)
		}
	}
	u, _, _ := d.ResolveModifiers(3, 1)
	v, err := d.Input(ctx, "NaN", u.Typmod)
	if err != nil || v.special != numericNaN {
		t.Fatal(v, err)
	}
	// Meaningful input typmods round before storage limits, unlike unknown input.
	u, _, _ = d.ResolveModifiers(1, 0)
	v, err = d.Input(ctx, "1e-20000", u.Typmod)
	if err != nil || v.text != "" {
		t.Fatal(v, err)
	}
}
func TestNumericComparisonAndConversions(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Numeric]
	for _, tc := range []struct {
		a, b string
		want int
	}{{"1.0", "1.00", 0}, {"9007199254740993", "9007199254740992", 1}, {"-0.1", "0", -1}, {"-Infinity", "-1", -1}, {"Infinity", "NaN", -1}, {"NaN", "NaN", 0}} {
		a, _ := d.Input(ctx, tc.a, -1)
		b, _ := d.Input(ctx, tc.b, -1)
		before := a
		p, _ := BindComparison(d, d, CollationRef{}, false)
		got, err := p.Compare(ctx, a, b, InputEnvironment{})
		if err != nil || got != tc.want || a != before {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		in  string
		id  ID
		out string
	}{
		{"1.5", Int2, "2"}, {"-1.5", Int2, "-2"},
		{"32767.49", Int2, "32767"}, {"-32768.49", Int2, "-32768"},
		{"1.5", Int4, "2"}, {"-1.5", Int8, "-2"},
		{"2147483647.49", Int4, "2147483647"}, {"-2147483648.49", Int4, "-2147483648"},
		{"9223372036854775807.49", Int8, "9223372036854775807"}, {"-9223372036854775808.49", Int8, "-9223372036854775808"},
		{"1.2500", Text, "1.2500"}, {"0.5", Float8, "0.5"},
	} {
		v, _ := d.Input(ctx, tc.in, -1)
		p, err := PlanCoercion(d.Usage(), types[tc.id].Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, _ := out.Type().Output(ctx, out)
		if text != tc.out {
			t.Fatal(tc, text)
		}
	}
	for _, tc := range []struct {
		in      string
		id      ID
		message string
	}{
		{"32767.5", Int2, "smallint out of range"}, {"-32768.5", Int2, "smallint out of range"},
		{"NaN", Int2, "cannot convert NaN to smallint"}, {"Infinity", Int2, "cannot convert infinity to smallint"},
		{"-Infinity", Int2, "cannot convert infinity to smallint"},
		{"2147483647.5", Int4, "integer out of range"}, {"-2147483648.5", Int4, "integer out of range"},
		{"9223372036854775807.5", Int8, "bigint out of range"}, {"-9223372036854775808.5", Int8, "bigint out of range"},
		{"NaN", Int4, "cannot convert NaN to integer"}, {"Infinity", Int8, "cannot convert infinity to bigint"},
	} {
		v, _ := d.Input(ctx, tc.in, -1)
		p, _ := PlanCoercion(d.Usage(), types[tc.id].Usage(), Assignment)
		_, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		code := "22003"
		if tc.in == "NaN" || tc.in == "Infinity" || tc.in == "-Infinity" {
			code = "0A000"
		}
		numericProviderDiagnostic(t, err, sqlstate.Error{Code: code, Message: tc.message})
	}
	for _, tc := range []struct {
		id      ID
		in, out string
	}{{Float4, "1.23456789", "1.23457"}, {Float8, "1.2345678901234567", "1.23456789012346"}} {
		v, _ := types[tc.id].Input(ctx, tc.in, -1)
		p, _ := PlanCoercion(types[tc.id].Usage(), d.Usage(), Assignment)
		out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, _ := d.Output(ctx, out)
		if text != tc.out {
			t.Fatal(text, tc)
		}
	}
	integer, _ := Integer(types[Int8], 9007199254740993)
	decimal, _ := d.Input(ctx, "9007199254740993.0", -1)
	p, _ := BindComparison(types[Int8], d, CollationRef{}, false)
	if n, err := p.Compare(ctx, integer, decimal, InputEnvironment{}); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	huge, _ := d.Input(ctx, "1e400", -1)
	f, _ := Float(types[Float8], 1)
	p, _ = BindComparison(d, types[Float8], CollationRef{}, false)
	_, err := p.Compare(ctx, huge, f, InputEnvironment{})
	numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: `"` + "1" + strings.Repeat("0", 400) + `" is out of range for type double precision`})
	equality, _ := BindComparisonPredicate(CompareEqual, d, d, types[Bool], CollationRef{}, false)
	null, err := equality.Evaluate(ctx, Null(d), decimal, InputEnvironment{})
	if err != nil || !null.IsNull() || null.Type() != types[Bool] {
		t.Fatal(null, err)
	}
	if _, err := PlanCoercion(types[Text].Usage(), d.Usage(), Implicit); err == nil {
		t.Fatal("implicit TEXT to numeric admitted")
	}
	if _, err := BindComparison(d, types[Bool], CollationRef{}, false); err == nil {
		t.Fatal("numeric BOOL comparison admitted")
	}
	real, _ := types[Float4].Input(ctx, "16777217", -1)
	known, _ := d.Input(ctx, "16777217.0", -1)
	comparison, _ := BindComparison(types[Float4], d, CollationRef{}, false)
	if n, err := comparison.Compare(ctx, real, known, InputEnvironment{}); err != nil || n != -1 {
		t.Fatal("known numeric narrowed to REAL", n, err)
	}
	wide, _ := d.Input(ctx, "1e100", -1)
	if n, err := comparison.Compare(ctx, real, wide, InputEnvironment{}); err != nil || n != -1 {
		t.Fatal("known numeric comparison selected REAL", n, err)
	}
	tiny, _ := d.Input(ctx, "1e-400", -1)
	cast, _ := PlanCoercion(d.Usage(), types[Float8].Usage(), Implicit)
	_, err = cast.Apply(ctx, tiny, InputEnvironment{}, OutputEnvironment{})
	numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: `"` + "0." + strings.Repeat("0", 399) + "1" + `" is out of range for type double precision`})
}

func numericProviderDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
}

func TestNumericFloatConversionBoundaryDiagnostics(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Numeric]
	for _, tc := range []struct {
		id                  ID
		literal, text, name string
	}{
		{Float4, "1e40", "1" + strings.Repeat("0", 40), "real"},
		{Float4, "1e-50", "0." + strings.Repeat("0", 49) + "1", "real"},
		{Float8, "1e400", "1" + strings.Repeat("0", 400), "double precision"},
		{Float8, "1e-400", "0." + strings.Repeat("0", 399) + "1", "double precision"},
	} {
		v, err := d.Input(ctx, tc.literal, -1)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanCoercion(d.Usage(), types[tc.id].Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		_, err = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type ` + tc.name})
	}
	for _, id := range []ID{Float4, Float8} {
		one, err := Float(types[id], 1)
		if err != nil {
			t.Fatal(err)
		}
		comparison, err := BindComparison(types[id], d, CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ literal, text string }{
			{"1e400", "1" + strings.Repeat("0", 400)},
			{"1e-400", "0." + strings.Repeat("0", 399) + "1"},
		} {
			v, err := d.Input(ctx, tc.literal, -1)
			if err != nil {
				t.Fatal(err)
			}
			_, err = comparison.Compare(ctx, one, v, InputEnvironment{})
			numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: `"` + tc.text + `" is out of range for type double precision`})
		}
	}
}

func TestNumericExistingSmallintCoercionAndComparison(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	small, numeric := types[Int2], types[Numeric]
	target, _, err := numeric.ResolveModifiers(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	cast, err := PlanCoercion(small.Usage(), target, Implicit)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{-32768, -2, 0, 2, 32767} {
		integer, err := Integer(small, n)
		if err != nil {
			t.Fatal(err)
		}
		// Precision 5, scale 2 fits three integer digits only.
		out, err := cast.Apply(ctx, integer, InputEnvironment{}, OutputEnvironment{})
		if n == -32768 || n == 32767 {
			numericProviderDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 5, scale 2 must round to an absolute value less than 10^3."})
		} else {
			if err != nil {
				t.Fatal(err)
			}
			text, err := numeric.Output(ctx, out)
			want := strconv.FormatInt(n, 10) + ".00"
			if err != nil || text != want {
				t.Fatal(text, err, want)
			}
		}
		exactCast, err := PlanCoercion(small.Usage(), numeric.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		exact, err := exactCast.Apply(ctx, integer, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := numeric.Output(ctx, exact)
		if err != nil || text != strconv.FormatInt(n, 10) {
			t.Fatal(text, err, n)
		}
		for _, reverse := range []bool{false, true} {
			left, right := integer, exact
			if reverse {
				left, right = right, left
			}
			equality, err := BindComparisonPredicate(CompareEqual, left.Type(), right.Type(), types[Bool], CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			equal, err := equality.Evaluate(ctx, left, right, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := equal.Boolean()
			if err != nil || !got {
				t.Fatal(got, err)
			}
			null, err := equality.Evaluate(ctx, Null(left.Type()), right, InputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != types[Bool] {
				t.Fatal(null, err)
			}
		}
	}
	null, err := cast.Apply(ctx, Null(small), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !null.IsNull() || null.Type() != numeric {
		t.Fatal(null, err)
	}
	integer, _ := Integer(small, 32767)
	for _, tc := range []struct {
		literal string
		order   int
	}{{"32767.00", 0}, {"32767.1", -1}, {"32768", -1}, {"-32769", 1}, {"1e100", -1}, {"NaN", -1}, {"Infinity", -1}, {"-Infinity", 1}} {
		decimal, err := numeric.Input(ctx, tc.literal, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, reverse := range []bool{false, true} {
			left, right, want := integer, decimal, tc.order
			if reverse {
				left, right, want = right, left, -want
			}
			comparison, err := BindComparison(left.Type(), right.Type(), CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := comparison.Compare(ctx, left, right, InputEnvironment{}); err != nil || got != want {
				t.Fatal(tc, reverse, got, err, want)
			}
		}
	}
}

func TestNumericSmallintAssignmentContextAndNull(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	small, numeric := types[Int2], types[Numeric]
	if _, err := PlanCoercion(numeric.Usage(), small.Usage(), Implicit); err == nil {
		t.Fatal("implicit numeric to smallint admitted")
	}
	cast, err := PlanCoercion(numeric.Usage(), small.Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	null, err := cast.Apply(ctx, Null(numeric), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !null.IsNull() || null.Type() != small {
		t.Fatal(null, err)
	}
	if _, _, ok := small.Usage().DecimalSize(); ok {
		t.Fatal("smallint gained numeric typmod metadata")
	}
	if _, err := PlanCoercion(types[UUID].Usage(), numeric.Usage(), Assignment); err == nil {
		t.Fatal("UUID to numeric admitted")
	}
	if _, err := PlanCoercion(numeric.Usage(), types[UUID].Usage(), Assignment); err == nil {
		t.Fatal("numeric to UUID admitted")
	}
}
