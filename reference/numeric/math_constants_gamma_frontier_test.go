package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathConstantsGammaFrontier(t *testing.T) {
	p := fixture.NewScenario(t)
	// Half-integer inputs are exact decimal witnesses, not host-rounded inputs.
	// No Go math.Gamma output or inferred errno is an expected frontier value.
	cases := []referenceMathConstantsProbe{
		{name: "negative_normal_neighbor", function: "gamma", input: "-169.5", category: "value", want: 5.6482208842233247e-306, ulps: 2},
		{name: "negative_170_half", function: "gamma", input: "-170.5", category: "value", want: -3.3127395215386069e-308, ulps: 2},
		{name: "negative_171_half", function: "gamma", input: "-171.5", category: "value", want: 1.9316265431711902e-310, ulps: 2},
		{name: "negative_subnormal_neighbor", function: "gamma", input: "-172.5", category: "value", want: -1.1197835032887635e-312, ulps: 2},
		{name: "negative_175_half", function: "gamma", input: "-175.5", category: "value", want: 2.1074864189004213e-319, ulps: 2},
		// Independent 110-digit decimal Stirling/reflection witnesses for the
		// represented input, not Go Gamma output or learned backend goldens.
		{name: "negative_quarter", function: "gamma", input: "-171.25", category: "value", want: 9.8910291950448e-310, ulps: 2},
		{name: "negative_three_quarters", function: "gamma", input: "-171.75", category: "value", want: 7.5418350735583e-311, ulps: 2},
		{name: "pole171_below", function: "gamma", input: "-171.00000000000003", category: "value", want: 2.835121658031596e-296, ulps: 2},
		{name: "pole171_above", function: "gamma", input: "-170.99999999999997", category: "value", want: -2.835121658032425e-296, ulps: 2},
		{name: "pole172_below", function: "gamma", input: "-172.00000000000003", category: "value", want: -1.6483265453672064e-298, ulps: 2},
		{name: "pole172_above", function: "gamma", input: "-171.99999999999997", category: "value", want: 1.648326545367689e-298, ulps: 2},
		{name: "negative_180_half", function: "gamma", input: "-180.5", category: "underflow"},
		{name: "negative_zero_result", function: "gamma", input: "-1000.5", category: "underflow"},
		{name: "positive_zero_pole", function: "gamma", input: "0", category: "overflow"},
		{name: "negative_zero_pole", function: "gamma", input: "-0", category: "overflow"},
		{name: "negative_one_pole", function: "gamma", input: "-1", category: "overflow"},
		{name: "negative_two_pole", function: "gamma", input: "-2", category: "overflow"},
		{name: "negative_infinity", function: "gamma", input: "-Infinity", category: "overflow"},
		{name: "positive_overflow", function: "gamma", input: "172", category: "overflow"},
	}
	referenceMathConstantsGammaCases(t, p, cases)
}

func referenceMathConstantsGammaCases(t *testing.T, p *fixture.Pair, cases []referenceMathConstantsProbe) {
	t.Helper()
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.name+"/literal", func(t *testing.T) { referenceMathConstantsProbeModes(t, p, backend, tc) })
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(prefix+"held", func(t *testing.T) {
				query := prefix + "SELECT gamma($1)"
				stmt, err := conn.PrepareContext(ctx, query)
				if err != nil {
					t.Errorf("held SQL=%q unexpected phase=prepare diagnostic=%#v", query, fixture.ExtractDiagnostic(t, err))
					return
				}
				defer stmt.Close()
				held := fixture.HeldStatement{conn, stmt, query}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						referenceMathConstantsProbeQuery(t, p, backend, held, query, "gamma", tc, tc.input)
					})
				}
			})
			t.Run(prefix+"sql_parameter", func(t *testing.T) {
				prepare := prefix + "PREPARE math_constants_gamma AS SELECT gamma($1)"
				if _, err := conn.ExecContext(ctx, prepare); err != nil {
					t.Errorf("SQL=%q unexpected phase=prepare diagnostic=%#v", prepare, fixture.ExtractDiagnostic(t, err))
					return
				}
				defer func() {
					if _, err := conn.ExecContext(ctx, "DEALLOCATE math_constants_gamma"); err != nil {
						t.Error(err)
					}
				}()
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						query := prefix + "EXECUTE math_constants_gamma('" + tc.input + "')"
						referenceMathConstantsProbeQuery(t, p, backend, conn, query, "gamma", tc)
					})
				}
			})
			for _, tc := range cases {
				t.Run(prefix+"sql_constant/"+tc.name, func(t *testing.T) {
					prepare := prefix + "PREPARE math_constants_gamma AS SELECT gamma('" + tc.input + "'::float8)"
					if _, err := conn.ExecContext(ctx, prepare); err != nil {
						referenceMathConstantsProbeError(t, tc, prepare, "prepare", err)
						return
					}
					defer func() {
						if _, err := conn.ExecContext(ctx, "DEALLOCATE math_constants_gamma"); err != nil {
							t.Error(err)
						}
					}()
					referenceMathConstantsProbeQuery(t, p, backend, conn, prefix+"EXECUTE math_constants_gamma", "gamma", tc)
				})
			}
		}
	})
}
