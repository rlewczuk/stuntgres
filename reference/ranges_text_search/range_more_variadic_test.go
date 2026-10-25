package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL accepts VARIADIC on these selected unary signatures. This does not
// introduce named-family or multiple-range constructor overloads.
func TestReferenceRangeMoreVariadic(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_variadic(a int4range,m int4multirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_variadic VALUES ('[1,4]','{[8,9),[1,3)}')", 1))
		for _, q := range []string{
			"SELECT multirange(VARIADIC a) AS v FROM more_variadic",
			"SELECT v FROM more_variadic,multirange(VARIADIC a) AS u(v)",
		} {
			moreBoth(t, p, b, rangeSelect(b, q, []string{"v"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}}, nil))
		}
		for _, q := range []string{
			"SELECT unnest(VARIADIC m) AS v FROM more_variadic",
			"SELECT v FROM more_variadic,unnest(VARIADIC m) AS u(v)",
		} {
			moreBoth(t, p, b, rangeSelect(b, q, []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[8,9)"}}, nil))
		}
	})
}
