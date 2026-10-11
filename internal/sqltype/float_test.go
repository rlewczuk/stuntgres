package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestFloatCodecs(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id   ID
		bits uint64
		out  string
	}{
		{Float4, 1, "1e-45"}, {Float4, 2, "3e-45"}, {Float4, 3, "4e-45"},
		{Float4, 0x4c000004, "3.3554448e+07"}, {Float4, 0x48951f84, "305404.12"}, {Float4, 0x39800000, "0.00024414062"}, {Float4, 0x15ae43fd, "7.038531e-26"},
		{Float4, 0x7f7fffff, "3.4028235e+38"}, {Float4, 0x00800000, "1.1754944e-38"},
		{Float8, 0x0010000000000000, "2.2250738585072014e-308"}, {Float8, 0x0010000000000001, "2.225073858507202e-308"},
		{Float8, 0x3fb999999999999a, "0.1"}, {Float8, 0x3fb9999999999999, "0.09999999999999999"}, {Float8, 0x3fb999999999999b, "0.10000000000000002"},
		{Float8, 1, "5e-324"}, {Float8, 0x7fefffffffffffff, "1.7976931348623157e+308"},
		{Float8, 0x4350000000000002, "1.8014398509481992e+16"},
		{Float8, 0xc350000000000002, "-1.8014398509481992e+16"},
	} {
		n := math.Float64frombits(tc.bits)
		if tc.id == Float4 {
			n = float64(math.Float32frombits(uint32(tc.bits)))
		}
		d := types[tc.id]
		v, err := Float(d, n)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.out {
			t.Fatalf("%x: %q (%v), want %q", tc.bits, out, err, tc.out)
		}
		back, err := d.Input(ctx, out, -1)
		if err != nil || math.Float64bits(back.floating) != math.Float64bits(n) {
			t.Fatalf("roundtrip %q: %v %v", out, back, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+out+"'" {
			t.Fatal(literal, err)
		}
	}
	for _, id := range []ID{Float4, Float8} {
		d := types[id]
		for _, tc := range []struct{ in, out string }{{"-0", "-0"}, {"0", "0"}, {"nan", "NaN"}, {" INF ", "Infinity"}, {"-inFINITY", "-Infinity"}, {"0.0001", "0.0001"}, {"0.00001", "1e-05"}} {
			v, err := d.Input(ctx, tc.in, -1)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.out {
				t.Fatal(tc, out, err)
			}
		}
		for _, in := range []string{"", " ", "1_0", "1x", "1 e2", "0x1p0", "+NaN", "NaN(payload)", "\u00a01"} {
			_, err := d.Input(ctx, in, -1)
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type " + map[ID]string{Float4: "real", Float8: "double precision"}[id] + ": \"" + in + "\""}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatalf("%q: %#v want %#v", in, diagnostic, want)
			}
		}
		for _, in := range []string{"1e999", "1e-999", "  1e999junk "} {
			_, err := d.Input(ctx, in, -1)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
				t.Fatal(in, err)
			}
		}
	}
	v, err := types[Float4].Input(ctx, "7.038531e-26", -1)
	if err != nil || math.Float32bits(float32(v.floating)) != 0x15ae43fd {
		t.Fatal(v, err)
	}
	parsed, _ := types[Float8].Input(ctx, "7.038531e-26", -1)
	narrow, _ := Float(types[Float4], parsed.floating)
	if math.Float32bits(float32(narrow.floating)) != 0x15ae43fe {
		t.Fatal("double rounding witness lost")
	}
	for _, tc := range []struct {
		id ID
		in string
	}{{Float4, "1e-46"}, {Float4, "3.4028236e38"}, {Float8, "1e-325"}, {Float8, "1.7976931348623159e308"}} {
		if _, err := types[tc.id].Input(ctx, tc.in, -1); err == nil {
			t.Fatal(tc)
		}
	}
	if out := floatOutput(1000000, Float4); out != "1e+06" {
		t.Fatal(out)
	}
	if out := floatOutput(1000000, Float8); out != "1000000" {
		t.Fatal(out)
	}
}

func TestFloatBoundaryCorpus(t *testing.T) {
	// Deterministic raw-bit corpus plus every power-of-two boundary and its
	// neighbors exercises asymmetric intervals and exponent extremes.
	check := func(n float64, id ID) {
		t.Helper()
		if math.IsInf(n, 0) || math.IsNaN(n) || n == 0 {
			return
		}
		size := 64
		if id == Float4 {
			size = 32
		}
		out := floatOutput(n, id)
		d := testTypes()[id]
		v, err := d.Input(context.Background(), out, -1)
		if err != nil || math.Float64bits(n) != math.Float64bits(v.floating) {
			t.Fatalf("%x %q: %v", math.Float64bits(n), out, err)
		}
		oracleAssertOpen(t, n, size, out)
	}
	for exp := uint32(1); exp < 255; exp++ {
		for _, delta := range []int64{-1, 0, 1} {
			check(float64(math.Float32frombits(uint32(int64(exp<<23)+delta))), Float4)
		}
	}
	for exp := uint64(1); exp < 2047; exp++ {
		for _, delta := range []int64{-1, 0, 1} {
			check(math.Float64frombits(uint64(int64(exp<<52)+delta)), Float8)
		}
	}
	seed := uint64(123456789)
	for range 10000 {
		seed = seed*6364136223846793005 + 1
		check(math.Float64frombits(seed), Float8)
		check(float64(math.Float32frombits(uint32(seed))), Float4)
	}
}

func TestFloatProvidersOwnership(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	a, _ := Float(types[Float4], 16777216)
	b, _ := Integer(types[Int4], 16777217)
	cmp, err := BindComparison(types[Float4], types[Int4], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := cmp.Compare(ctx, a, b, InputEnvironment{}); err != nil || n != -1 {
		t.Fatal(n, err)
	}
	for _, pair := range [][2]ID{{Float4, Float8}, {Float8, Float4}, {Float4, Float4}} {
		left, _ := Float(types[pair[0]], math.NaN())
		right, _ := Float(types[pair[1]], math.NaN())
		eq, err := BindComparisonPredicate(CompareEqual, left.Type(), right.Type(), types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		v, err := eq.Evaluate(ctx, left, right, InputEnvironment{})
		if err != nil || !v.boolean {
			t.Fatal(v, err)
		}
		v, err = eq.Evaluate(ctx, left, Null(right.Type()), InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != types[Bool] {
			t.Fatal(v, err)
		}
	}
	for _, pair := range [][2]ID{{Int4, Float4}, {Int8, Float8}, {Float4, Float8}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Implicit); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PlanCoercion(types[Float8].Usage(), types[Float4].Usage(), Implicit); err == nil {
		t.Fatal("implicit narrowing")
	}
	plan, err := PlanCoercion(types[Float8].Usage(), types[Float4].Usage(), Assignment)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []float64{math.MaxFloat64, math.SmallestNonzeroFloat64} {
		v, _ := Float(types[Float8], n)
		if _, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("range accepted", n)
		}
	}
	foreign, _ := Float(testTypes()[Float8], 1)
	if _, err := plan.Apply(ctx, foreign, InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("foreign value")
	}
	if _, err := types[Float4].Output(ctx, foreign); err == nil {
		t.Fatal("foreign output")
	}
	for _, v := range []Value{{}, Null(types[Float4]), String(types[Text], "x")} {
		if _, err := v.Float(); err == nil {
			t.Fatal("invalid accessor")
		}
	}
	if _, err := Float(types[Int4], 1); err == nil {
		t.Fatal("invalid constructor")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := types[Float4].Input(canceled, "1", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := cmp.Compare(canceled, a, b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFloatCoercionsAndExactDiagnostics(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Float4, Float8} {
		d := types[id]
		name := "real"
		if id == Float8 {
			name = "double precision"
		}
		for _, tc := range []struct{ input, code, message string }{{"  1e999junk ", "22003", `"1e999" is out of range for type ` + name}, {"  1junk ", "22P02", `invalid input syntax for type ` + name + `: "  1junk "`}} {
			_, err := d.Input(ctx, tc.input, -1)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &got) || *got != want {
				t.Fatal(got, want)
			}
		}
		zero, err := d.Input(ctx, "-0e-999", -1)
		if err != nil || !math.Signbit(zero.floating) {
			t.Fatal(zero, err)
		}
		plan, err := PlanCoercion(types[Int8].Usage(), d.Usage(), Implicit)
		if err != nil {
			t.Fatal(err)
		}
		integer, _ := Integer(types[Int8], 16777217)
		v, err := plan.Apply(ctx, integer, InputEnvironment{}, OutputEnvironment{})
		want := float64(16777217)
		if id == Float4 {
			want = 16777216
		}
		if err != nil || v.floating != want {
			t.Fatal(v, err)
		}
		ioPlan, err := PlanCoercion(d.Usage(), types[Text].Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		text, err := ioPlan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || text.text == "" {
			t.Fatal(text, err)
		}
		null, err := plan.Apply(ctx, Null(types[Int8]), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != d {
			t.Fatal(null, err)
		}
		positive, _ := Float(d, 0)
		negative, _ := Float(d, math.Copysign(0, -1))
		cmp, _ := BindComparison(d, d, CollationRef{}, false)
		if n, err := cmp.Compare(ctx, positive, negative, InputEnvironment{}); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	// Integer-to-preferred-float8 conversion intentionally loses integer bits.
	a, _ := Float(types[Float8], 9007199254740992)
	b, _ := Integer(types[Int8], 9007199254740993)
	cmp, _ := BindComparison(a.Type(), b.Type(), CollationRef{}, false)
	if n, err := cmp.Compare(ctx, a, b, InputEnvironment{}); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	x, _ := Integer(types[Int8], 9007199254740992)
	exact, _ := BindComparison(x.Type(), b.Type(), CollationRef{}, false)
	if n, err := exact.Compare(ctx, x, b, InputEnvironment{}); err != nil || n != -1 {
		t.Fatal(n, err)
	}
}
