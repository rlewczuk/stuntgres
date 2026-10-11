package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomDeterministicResults(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct {
		q, name, typ string
		value        any
	}{
		{"SELECT random(7,7)", "random", "INT4", int64(7)},
		{"SELECT random(2147483648,2147483648)", "random", "INT8", int64(2147483648)},
		{"SELECT random(1.0,1.000)", "random", "NUMERIC", "1.000"},
		{"SELECT random(0.00,0.000)", "random", "NUMERIC", "0.000"},
		{"SELECT random_normal(5,0)", "random_normal", "FLOAT8", float64(5)},
		{"SELECT random_normal(NULL,1)", "random_normal", "FLOAT8", nil},
	} {
		s := fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.value}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}

// Values are checked against independent ranges and each backend's own reseed
// witness. No random draws are compared between PostgreSQL and native.
