package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func cubeUnitType(t *testing.T, id ID) *Descriptor {
	t.Helper()
	d, err := Cube(id, "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestCubeInput(t *testing.T) {
	d := cubeUnitType(t, 45000)
	for _, tc := range []struct {
		text      string
		dimension int
		point     bool
		bits      []uint64
	}{
		{"()", 0, true, nil}, {"(),()", 0, true, nil},
		{"(3,1),(1,4)", 2, false, []uint64{0x4008000000000000, 0x3ff0000000000000, 0x3ff0000000000000, 0x4010000000000000}},
		{"(-0),(0)", 1, true, []uint64{1 << 63}}, {"(0),(-0)", 1, true, []uint64{0}},
		{"5e-324,0.10000000000000002", 2, true, []uint64{1, 0x3fb999999999999b}},
		{"2.2250738585072014e-308,1.7976931348623157e308", 2, true, []uint64{0x0010000000000000, 0x7fefffffffffffff}},
		{"(Infinity),(Infinity)", 1, true, []uint64{0x7ff0000000000000}},
	} {
		v, err := d.Input(context.Background(), tc.text, -1)
		if err != nil {
			t.Fatal(tc.text, err)
		}
		if !v.validCube() || v.cube.dimension != tc.dimension || v.cube.point != tc.point || len(v.cube.coordinates) != len(tc.bits) {
			t.Fatal(tc.text, v.cube)
		}
		for i, bits := range tc.bits {
			if math.Float64bits(v.cube.coordinates[i]) != bits {
				t.Fatal(tc.text, i, v.cube.coordinates[i], bits)
			}
		}
	}
	nan, err := d.Input(context.Background(), "(NaN),(NaN)", -1)
	if err != nil || nan.cube.point || !math.IsNaN(nan.cube.coordinates[0]) || !math.IsNaN(nan.cube.coordinates[1]) {
		t.Fatal("NaN must not collapse", nan, err)
	}
	hundred := "(" + strings.Repeat("1,", 99) + "1)"
	v, err := d.Input(context.Background(), hundred, -1)
	if err != nil || v.cube.dimension != 100 {
		t.Fatal("100-dimensional boundary", err)
	}
	for _, tc := range []struct{ text, code, detail string }{
		{"ABC", "22P02", `syntax error at or near "A"`}, {"1..2", "22P02", `syntax error at or near ".2"`},
		{"+NaN", "22P02", `syntax error at or near "+"`}, {"(1,)", "22P02", `syntax error at or near ")"`},
		{"(1,2),(3)junk", "22P02", "Different point dimensions in (1,2) and (3)."},
		{"(" + strings.Repeat("1,", 100) + "1)junk", "22P02", "A cube cannot have more than 100 dimensions."},
		{"1e999", "22003", ""}, {"-1e-700", "22003", ""},
	} {
		_, err := d.Input(context.Background(), tc.text, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Detail != tc.detail {
			t.Fatal(tc.text, err)
		}
	}
}
func TestCubeOutput(t *testing.T) {
	d := cubeUnitType(t, 45000)
	for _, tc := range []struct{ in, out string }{
		{"[(3,1),(1,4)]", "(3, 1),(1, 4)"}, {"1e27,1e-7", "(1e+27, 1e-07)"},
		{"NaN,-Infinity", "(NaN, -Infinity)"}, {"-0", "(-0)"},
		{"0.10000000000000002", "(0.10000000000000002)"},
	} {
		v, err := d.Input(context.Background(), tc.in, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Output(context.Background(), v)
		if err != nil || got != tc.out {
			t.Fatal(tc.in, got, err)
		}
	}
	v, err := d.Input(context.Background(), "0.10000000000000002", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := d.OutputWithEnvironment(context.Background(), v, OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: 0})
	if err != nil || text != "(0.1)" {
		t.Fatal(text, err)
	}
	literal, err := d.SQLLiteral(context.Background(), v)
	if err != nil || literal != "'(0.10000000000000002)'" {
		t.Fatal("dump must not use display precision", literal, err)
	}
}
func TestCubeIdentity(t *testing.T) {
	d := cubeUnitType(t, 45000)
	reminted := cubeUnitType(t, 45000)
	foreign := cubeUnitType(t, 45001)
	if !d.Concrete() || !d.IsUserDefined() || d.IsBuiltin() || d.DatabaseName() != "CUBE" {
		t.Fatal("scalar authority")
	}
	if _, err := Cube(0, "cube", "cube"); err == nil {
		t.Fatal("invalid descriptor admitted")
	}
	v, err := d.Input(context.Background(), "(3,1),(1,4)", -1)
	if err != nil {
		t.Fatal(err)
	}
	same, err := v.Retag(reminted)
	if err != nil || same.cube != v.cube {
		t.Fatal("immutable retag", err)
	}
	if _, err := v.Retag(foreign); err == nil {
		t.Fatal("Retag must not import foreign OID")
	}
	mapped, err := v.RemapCube(foreign)
	if err != nil || mapped.cube != v.cube || mapped.Type() != foreign || v.Type() != d {
		t.Fatal("immutable import", err)
	}
	if _, err := v.RemapCube(&Descriptor{id: 45002}); err == nil {
		t.Fatal("foreign family admitted")
	}
	if _, err := (Value{typ: d}).Retag(reminted); err == nil {
		t.Fatal("missing payload admitted")
	}
	n, err := Null(d).RemapCube(foreign)
	if err != nil || !n.IsNull() || n.Type() != foreign {
		t.Fatal("typed NULL import", err)
	}
	a := LookupArray(45003, "_cube", d)
	if a.Concrete() {
		t.Fatal("lookup array became storage")
	}
	if _, err := Array(45004, "_cube", d); err == nil {
		t.Fatal("cube array storage admitted")
	}
	facts := d.AttributeFacts()
	if facts.Length != -1 || facts.Alignment != "d" || facts.Storage != "p" || facts.ByValue {
		t.Fatal(facts)
	}
}
func TestCubeCoercion(t *testing.T) {
	d := cubeUnitType(t, 45000)
	text := &Descriptor{id: Text}
	varchar := &Descriptor{id: VarChar}
	for _, character := range []*Descriptor{text, varchar} {
		for _, tc := range []struct {
			from, to *Descriptor
			context  CoercionContext
			allowed  bool
		}{
			{character, d, Explicit, true}, {character, d, Assignment, false}, {d, character, Assignment, true},
			{d, character, Explicit, true}, {d, character, Implicit, false}, {character, d, Implicit, false},
		} {
			_, err := PlanCoercion(tc.from.Usage(), tc.to.Usage(), tc.context)
			if (err == nil) != tc.allowed {
				t.Fatal(tc.from, tc.to, tc.context, err)
			}
		}
	}
}
func TestCubeCancellation(t *testing.T) {
	d := cubeUnitType(t, 45000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Input(ctx, "1", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	v, err := d.Input(context.Background(), "1", -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Output(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCubeDiagnosticStages(t *testing.T) {
	d := cubeUnitType(t, 45000)
	const eof = "syntax error at end of input"
	const dimensions = "A cube cannot have more than 100 dimensions."
	overlimit := "(" + strings.Repeat("1,", 100) + "1e999)"
	for _, tc := range []struct{ name, text, code, message, detail string }{
		{"incomplete_point", "(1e999", "22P02", "invalid input syntax for cube", eof},
		{"incomplete_second_corner", "(1),(1e999", "22P02", "invalid input syntax for cube", eof},
		{"missing_bracket", "[(1e999),(2)", "22P02", "invalid input syntax for cube", eof},
		{"missing_bracket_mismatch", "[(1,2),(3)", "22P02", "invalid input syntax for cube", eof},
		{"point_limit", overlimit, "22P02", "invalid input syntax for cube", dimensions},
		{"box_limit", "[" + overlimit + "," + overlimit + "]", "22P02", "invalid input syntax for cube", dimensions},
		{"limit_before_suffix", overlimit + "junk", "22P02", "invalid input syntax for cube", dimensions},
		{"mismatch_before_conversion", "(1e999,2),(3)junk", "22P02", "invalid input syntax for cube", "Different point dimensions in (1e999,2) and (3)."},
		{"point_conversion_before_suffix", "(1e999)junk", "22003", `"1e999" is out of range for type double precision`, ""},
		{"box_conversion_before_suffix", "[(1e999),(2)]junk", "22003", `"1e999" is out of range for type double precision`, ""},
		{"valid_point_suffix", "(1)junk", "22P02", "invalid input syntax for cube", `syntax error at or near "j"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := d.Input(context.Background(), tc.text, -1)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Detail != tc.detail {
				t.Fatalf("got %#v (%v), want %s / %s / %s", diagnostic, err, tc.code, tc.message, tc.detail)
			}
			if v.cube != nil {
				t.Fatal("failed input published a cube payload")
			}
		})
	}
	t.Run("recognize_without_conversion", func(t *testing.T) {
		s := cubeScanner{text: " 1e999 , -0 )suffix"}
		var tokens [100]string
		n, span, err := s.list(context.Background(), tokens[:], true)
		if err != nil || n != 2 || span != "1e999 , -0 " || tokens[0] != "1e999" || tokens[1] != "-0" || s.text[s.pos:] != "suffix" {
			t.Fatalf("list n=%d span=%q tokens=%q/%q suffix=%q err=%v", n, span, tokens[0], tokens[1], s.text[s.pos:], err)
		}
	})
	t.Run("legal_payload_unchanged", func(t *testing.T) {
		v, err := d.Input(context.Background(), "[ ( -0 , 5e-324 ),( 2 , 3 ) ]", -1)
		if err != nil {
			t.Fatal(err)
		}
		want := []uint64{1 << 63, 1, 0x4000000000000000, 0x4008000000000000}
		if !v.validCube() || v.cube.point || v.cube.dimension != 2 {
			t.Fatal("box representation", v.cube)
		}
		for i, bits := range want {
			if math.Float64bits(v.cube.coordinates[i]) != bits {
				t.Fatalf("coordinate %d bits=%x want %x", i, math.Float64bits(v.cube.coordinates[i]), bits)
			}
		}
	})
}

func TestCubeMismatchDetail(t *testing.T) {
	d := cubeUnitType(t, 45000)
	long := strings.Repeat("1,", 100) + "1e999"
	for _, tc := range []struct{ text, first, second string }{
		{"( 1 , 2 ),( 3 )", "1,2", "3"},
		{"[\t(\n1\r,\f2\v ) , (\t3\r\n )\f]", "1,2", "3"},
		{"( +01.0e+0 , .20E1 ),( -3. )", "+01.0e+0,.20E1", "-3."},
		{"( ),( -0 )", "", "-0"},
		{"(" + strings.ReplaceAll(long, ",", " , \t") + " ),( 3 )", long, "3"},
	} {
		_, err := d.Input(context.Background(), tc.text, -1)
		var diagnostic *sqlstate.Error
		want := "Different point dimensions in (" + tc.first + ") and (" + tc.second + ")."
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != "invalid input syntax for cube" || diagnostic.Detail != want {
			t.Fatalf("input %q: got %#v (%v), want %q", tc.text, diagnostic, err, want)
		}
	}
	// Diagnostic normalization must not repair separated numeric fragments.
	_, err := d.Input(context.Background(), "(1 e7,2),(3)", -1)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Detail != `syntax error at or near "e"` {
		t.Fatalf("separated numeric fragment: %#v (%v)", diagnostic, err)
	}
}

func TestCubeSameDatum(t *testing.T) {
	cube := cubeUnitType(t, 45000)
	datum := func(dimension int, point bool, bits ...uint64) Value {
		coordinates := make([]float64, len(bits))
		for i, bitPattern := range bits {
			coordinates[i] = math.Float64frombits(bitPattern)
		}
		return Value{typ: cube, cube: &cubePayload{dimension: dimension, point: point, coordinates: coordinates}}
	}
	for _, tc := range []struct {
		name string
		a, b Value
		same bool
	}{
		{"empty", datum(0, true), datum(0, true), true},
		{"point", datum(1, true, 0x3ff0000000000000), datum(1, true, 0x3ff0000000000000), true},
		{"box", datum(1, false, 0x3ff0000000000000, 0x4000000000000000), datum(1, false, 0x3ff0000000000000, 0x4000000000000000), true},
		{"orientation", datum(1, false, 0x3ff0000000000000, 0x4000000000000000), datum(1, false, 0x4000000000000000, 0x3ff0000000000000), false},
		{"point_flag", datum(1, true, 0x3ff0000000000000), datum(1, false, 0x3ff0000000000000, 0x3ff0000000000000), false},
		{"dimension", datum(1, true, 0x3ff0000000000000), datum(2, true, 0x3ff0000000000000, 0x4000000000000000), false},
		{"coordinate", datum(1, true, 0x3ff0000000000000), datum(1, true, 0x4000000000000000), false},
		{"signed_zero", datum(1, true, 0), datum(1, true, 1<<63), false},
		{"same_nan_bits", datum(1, true, 0x7ff8000000000001), datum(1, true, 0x7ff8000000000001), true},
		{"distinct_nan_bits", datum(1, true, 0x7ff8000000000001), datum(1, true, 0x7ff8000000000002), false},
		{"infinity", datum(1, true, 0x7ff0000000000000), datum(1, true, 0x7ff0000000000000), true},
		{"missing_payload", Value{typ: cube}, Value{typ: cube}, false},
		{"negative_dimension", datum(-1, true), datum(-1, true), false},
		{"excess_dimension", datum(101, true), datum(101, true), false},
		{"short_point", datum(2, true, 0), datum(2, true, 0), false},
		{"short_box", datum(1, false, 0), datum(1, false, 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameDatum(tc.a, tc.b); got != tc.same {
				t.Fatal("cube structural identity", got, tc.same)
			}
			if got := SameDatum(tc.b, tc.a); got != tc.same {
				t.Fatal("asymmetric cube structural identity", got, tc.same)
			}
			if tc.a.validCube() && !SameDatum(tc.a, tc.a) {
				t.Fatal("valid immutable self identity rejected")
			}
			if !tc.a.validCube() && SameDatum(tc.a, tc.a) {
				t.Fatal("shared invalid payload bypassed validation")
			}
		})
	}
	input := func(d *Descriptor, text string) Value {
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a, b := input(cube, "(1,2),(3,4)"), input(cube, "(1,2),(3,4)")
	if a.cube == b.cube || !SameDatum(a, b) {
		t.Fatal("separately owned equivalent cube payloads must match")
	}
	foreign := cubeUnitType(t, cube.ID())
	if SameDatum(a, input(foreign, "(1,2),(3,4)")) || SameDatum(Null(cube), Null(foreign)) || SameDatum(Value{}, Value{}) {
		t.Fatal("foreign or absent descriptor identity accepted")
	}
	if !SameDatum(Null(cube), Null(cube)) || SameDatum(a, Null(cube)) || SameDatum(Null(cube), a) {
		t.Fatal("cube NULL identity does not follow exact-descriptor gates")
	}
	domain, err := Domain(45010, "cube_domain", "public.cube_domain", cube.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Domain(45011, "nested_cube_domain", "public.nested_cube_domain", domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []*Descriptor{domain, nested} {
		left, right := input(d, "(1,2),(3,4)"), input(d, "(1,2),(3,4)")
		if !SameDatum(left, right) || !SameDatum(left, left) || SameDatum(left, a) || SameDatum(left, input(d, "(3,4),(1,2)")) {
			t.Fatal("domain-over-cube structural identity")
		}
		if !SameDatum(Null(d), Null(d)) || SameDatum(Null(d), Null(cube)) {
			t.Fatal("domain cube NULL descriptor distinction")
		}
		invalid := Value{typ: d, cube: &cubePayload{dimension: 1, point: true}}
		if SameDatum(invalid, invalid) {
			t.Fatal("shared invalid domain cube payload accepted")
		}
	}
	foreignDomain, err := Domain(domain.ID(), "cube_domain", "public.cube_domain", cube.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if SameDatum(input(domain, "1"), input(foreignDomain, "1")) || SameDatum(Null(domain), Null(foreignDomain)) {
		t.Fatal("foreign domain descriptor generation accepted")
	}
}
