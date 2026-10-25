package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantHyperbolicAuthority(t *testing.T) {
	// These exact mathematical identities need no host math implementation or
	// tolerance. Each target is its own released FLOAT8 -> FLOAT8 declaration.
	for _, tc := range []struct {
		name, input string
		argument    float64
		want        float64
	}{
		{"sinh", "0", 0, 0},
		{"cosh", "0", 0, 1},
		{"tanh", "0", 0, 0},
		{"asinh", "0", 0, 0},
		{"acosh", "1", 1, 0},
		{"atanh", "0", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_hyperbolic_source(id INT,x DOUBLE PRECISION,z DOUBLE PRECISION)", 0))
				run(fixture.Exec("INSERT INTO og_hyperbolic_source VALUES(1,"+tc.input+",0),(2,NULL,0)", 2))
				run(fixture.Exec("GRANT SELECT ON og_hyperbolic_source TO og_object_reader", 0))
				call := tc.name + "(x)"
				run(fixture.Exec("CREATE VIEW og_hyperbolic_view AS SELECT "+call+" AS v FROM og_hyperbolic_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_hyperbolic_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_hyperbolic_source"+tail, []string{"v"}, []string{"FLOAT8"}, [][]any{{want}}, args...)
				}
				cases := []objectGrantProviderCase{
					// Genuine row-backed calls and stored recipes, including the SAME
					// held statement, must consult the current invoker after revocation.
					{value(call, " WHERE id=1", tc.want), true, true},
					{value("pg_catalog."+call, " WHERE id=1", tc.want), true, true},
					{fixture.Select("SELECT v FROM og_hyperbolic_view", []string{"v"}, []string{"FLOAT8"}, [][]any{{tc.want}}), true, true},
					{value(tc.name+"(CASE WHEN id=1 THEN $1::DOUBLE PRECISION ELSE x END)", " WHERE id=1", tc.want, tc.argument), true, true},

					// A runtime NULL column is not a statically pruned strict call.
					{value(call, " WHERE id=2", nil), true, true},
					{value(tc.name+"(NULL::DOUBLE PRECISION)", " WHERE id=1", nil), false, false},
					{value(tc.name+"($1::DOUBLE PRECISION)", " WHERE id=1", nil, nil), false, false},

					// IMMUTABLE constant-only calls can be folded into PostgreSQL's
					// held plan. Assert their current authority only on fresh execution.
					{value(tc.name+"("+tc.input+"::DOUBLE PRECISION)", " WHERE id=1", tc.want), true, false},
					{value(tc.name+"($1::DOUBLE PRECISION)", " WHERE id=1", tc.want, tc.argument), true, false},
					{value("CASE WHEN FALSE THEN "+call+" ELSE NULL::DOUBLE PRECISION END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_hyperbolic_source WHERE id>99", []string{"v"}, []string{"FLOAT8"}, nil), true, true},
					{fixture.Select("SELECT "+call+" AS v FROM og_hyperbolic_source WHERE FALSE", []string{"v"}, []string{"FLOAT8"}, nil), true, true},
				}
				// Revocation of one exact hyperbolic identity cannot deny a sibling.
				// The separate zero column keeps both controls exact, even for acosh.
				sibling, siblingWant := "sinh(z)", float64(0)
				if tc.name == "sinh" {
					sibling, siblingWant = "cosh(z)", 1
				}
				controls := []fixture.Step{value(sibling, " WHERE id=1", siblingWant)}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+tc.name+"(double precision)", "function", tc.name, cases, controls)
			})
		})
	}
}
