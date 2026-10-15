package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var referenceHyperbolicNames = []string{"sinh", "cosh", "tanh", "asinh", "acosh", "atanh"}

func referenceHyperbolicBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}

func referenceHyperbolicFloats(t *testing.T, p *fixture.Pair, backend fixture.Backend, cases []referenceScalarFloatCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
			}
		})
	}
}

func referenceHyperbolicCase(name, input string, want float64, ulps uint64) referenceScalarFloatCase {
	return referenceScalarFloatCase{name + "_" + input, "SELECT " + name + "(" + input + ") AS result", "FLOAT8", want, ulps}
}

func TestReferenceMathHyperbolicValues(t *testing.T) {
	p := fixture.NewScenario(t)
	// Table 9.8's independently published examples, not provider outputs.
	cases := []referenceScalarFloatCase{
		referenceHyperbolicCase("sinh", "1", 1.1752011936438014, 2),
		referenceHyperbolicCase("cosh", "0", 1, 0),
		referenceHyperbolicCase("tanh", "1", 0.7615941559557649, 2),
		referenceHyperbolicCase("asinh", "1", 0.881373587019543, 2),
		referenceHyperbolicCase("acosh", "1", 0, 0),
		referenceHyperbolicCase("atanh", "'0.5'", 0.5493061443340548, 2),
	}
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		referenceHyperbolicFloats(t, p, backend, cases)
		for _, name := range referenceHyperbolicNames {
			input, want := "0", float64(0)
			if name == "acosh" {
				input = "1"
			}
			if name == "cosh" {
				want = 1
			}
			for _, typ := range []string{"SMALLINT", "INTEGER", "BIGINT", "NUMERIC", "REAL", "DOUBLE PRECISION"} {
				referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{referenceHyperbolicCase(name, input+"::"+typ, want, 0)})
			}
			for _, spelling := range []string{name, "pg_catalog." + name, `"` + name + `"`, map[string]string{"sinh": "SiNh", "cosh": "CoSh", "tanh": "TaNh", "asinh": "AsInH", "acosh": "AcOsH", "atanh": "AtAnH"}[name]} {
				referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{referenceHyperbolicCase(spelling, input, want, 0)})
			}
			referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+"($1)", []string{name}, []string{"FLOAT8"}, [][]any{{want}}, input))
			referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+"($1)", []string{name}, []string{"FLOAT8"}, [][]any{{nil}}, nil))
		}
	})
}

func TestReferenceMathHyperbolicIdentities(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range []struct {
			input string
			want  float64
		}{{"-1", -1}, {"-0.5", -0.5}, {"0", 0}, {"0.5", 0.5}, {"1", 1}} {
			for _, pair := range []struct {
				expression string
				want       float64
			}{
				{"asinh(sinh('" + tc.input + "'))", tc.want},
				{"acosh(cosh('" + tc.input + "'))", math.Abs(tc.want)},
				{"atanh(tanh('" + tc.input + "'))", tc.want},
			} {
				// Compare the FLOAT8 result outside SQL so this identity witness
				// does not require adjacent arithmetic grammar capabilities.
				// At these magnitudes 8ULP bounds absolute residual below 2e-15;
				// the zero case remains exact with the expected positive sign.
				ulps := uint64(8)
				if pair.want == 0 {
					ulps = 0
				}
				referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{{
					pair.expression, "SELECT " + pair.expression + " AS result", "FLOAT8", pair.want, ulps,
				}})
			}
		}
	})
}

func TestReferenceMathHyperbolicFrontiers(t *testing.T) {
	p := fixture.NewScenario(t)
	var cases []referenceScalarFloatCase
	// Independently rounded mathematical values, validated against pinned PG18.0.
	// The 710 finite category is not host variance.
	for _, tc := range []struct {
		input string
		want  float64
	}{{"709", 4.109203730777486e307}, {"710", 1.1169973830808555e308}, {"711", math.Inf(1)}, {"1000", math.Inf(1)}} {
		for _, name := range []string{"sinh", "cosh"} {
			cases = append(cases, referenceHyperbolicCase(name, "'"+tc.input+"'", tc.want, 2))
			want := tc.want
			if name == "sinh" {
				want = -want
			}
			cases = append(cases, referenceHyperbolicCase(name, "'-"+tc.input+"'", want, 2))
		}
	}
	for _, sign := range []string{"", "-"} {
		want := float64(1)
		if sign == "-" {
			want = -1
		}
		cases = append(cases, referenceHyperbolicCase("tanh", "'"+sign+"1.7976931348623157e308'", want, 0))
		cases = append(cases, referenceHyperbolicCase("asinh", "'"+sign+"1.7976931348623157e308'", want*710.4758600739439, 2))
	}
	cases = append(cases, referenceHyperbolicCase("acosh", "'1.7976931348623157e308'", 710.4758600739439, 2), referenceHyperbolicCase("acosh", "'1.0000000000000002'", 2.1073424255447017e-8, 2))
	for _, sign := range []string{"", "-"} {
		want := 18.714973875118524
		if sign == "-" {
			want = -want
		}
		cases = append(cases, referenceHyperbolicCase("atanh", "'"+sign+"0.9999999999999999'", want, 2))
	}
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) { referenceHyperbolicFloats(t, p, backend, cases) })
}

func TestReferenceMathHyperbolicSpecialValues(t *testing.T) {
	p := fixture.NewScenario(t)
	var cases []referenceScalarFloatCase
	for _, name := range referenceHyperbolicNames {
		cases = append(cases, referenceHyperbolicCase(name, "'NaN'", math.NaN(), 0))
		if name == "acosh" {
			cases = append(cases, referenceHyperbolicCase(name, "1", 0, 0), referenceHyperbolicCase(name, "'Infinity'", math.Inf(1), 0))
			continue
		}
		for _, sign := range []float64{1, -1} {
			zero := math.Copysign(0, sign)
			input := "'0'"
			if sign < 0 {
				input = "'-0'"
			}
			want := zero
			if name == "cosh" {
				want = 1
			}
			cases = append(cases, referenceHyperbolicCase(name, input, want, 0))
			if name == "atanh" {
				cases = append(cases, referenceHyperbolicCase(name, strconv.FormatFloat(sign, 'g', -1, 64), math.Inf(int(sign)), 0))
				continue
			}
			want = math.Inf(int(sign))
			if name == "cosh" {
				want = math.Inf(1)
			}
			if name == "tanh" {
				want = sign
			}
			input = "'Infinity'"
			if sign < 0 {
				input = "'-Infinity'"
			}
			cases = append(cases, referenceHyperbolicCase(name, input, want, 0))
		}
	}
	// Pinned PG18.0 preserves signed smallest subnormals for these odd functions.
	// Keep exact category/sign assertions; POSIX permits host libm variation.
	for _, name := range []string{"sinh", "tanh", "asinh", "atanh", "cosh"} {
		for _, sign := range []float64{1, -1} {
			input := "'5e-324'"
			if sign < 0 {
				input = "'-5e-324'"
			}
			want := sign * math.SmallestNonzeroFloat64
			if name == "cosh" {
				want = 1
			}
			cases = append(cases, referenceHyperbolicCase(name, input, want, 0))
		}
	}
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		referenceHyperbolicFloats(t, p, backend, cases)
		for _, name := range referenceHyperbolicNames {
			for _, input := range []string{"NULL", "NULL::double precision"} {
				referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+"("+input+")", []string{name}, []string{"FLOAT8"}, [][]any{{nil}}))
			}
		}
	})
}

func TestReferenceMathHyperbolicMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE hyperbolic_metadata(id INT,d DOUBLE PRECISION)", 0))
		for _, name := range referenceHyperbolicNames {
			empty := fixture.Select("SELECT "+name+"(d) FROM hyperbolic_metadata", []string{name}, []string{"FLOAT8"}, nil)
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
			referenceMathTrigStep(t, p, backend, empty)
			null := fixture.Select("SELECT "+name+"(NULL)", []string{name}, []string{"FLOAT8"}, [][]any{{nil}})
			null.CheckMetadata = empty.CheckMetadata
			referenceMathTrigStep(t, p, backend, null)
			input, want := "0", float64(0)
			if name == "acosh" {
				input = "1"
			}
			if name == "cosh" {
				want = 1
			}
			value := fixture.Select("SELECT "+name+"("+input+")", []string{name}, []string{"FLOAT8"}, [][]any{{want}})
			value.CheckMetadata = empty.CheckMetadata
			referenceMathTrigStep(t, p, backend, value)
		}
		referenceMathTrigStep(t, p, backend, referenceHyperbolicDomain("SELECT acosh(0) FROM hyperbolic_metadata"))
	})
}
