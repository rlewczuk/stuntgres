package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextPatternEscape(t *testing.T) {
	for _, rhs := range []string{"citext", "text"} {
		for _, escape := range []string{"citext", "text"} {
			t.Run(rhs+"/"+escape, func(t *testing.T) {
				citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					citextInstall(t, p, b, c)
					for _, op := range citextPatternOps[4:] {
						q := "SELECT $1::citext " + op.sql + " $2::" + rhs + " ESCAPE $3::" + escape + " AS v"
						for _, tc := range []struct {
							s, p, e any
							want    any
						}{
							{"A%B", "a!%b", "!", !op.negative}, {"A_B", "a!_b", "!", !op.negative}, {"A!B", "a!!b", "!", !op.negative},
							{"A%B", "a%b", "", !op.negative}, {`A\B`, `a\b`, "", !op.negative},
							{"", "\\", "\\", op.negative}, {"x", "z\\", "\\", op.negative},
							{nil, "[", "!", nil}, {"ABC", nil, "!", nil}, {"ABC", "abc", nil, nil},
						} {
							citextPatternRoutes(t, p, b, c, citextPatternBool(q, tc.want, tc.s, tc.p, tc.e))
						}
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'A%B'::citext "+op.sql+" $1::"+rhs+" AS v", !op.negative, `a\%b`))
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'A_B'::citext "+op.sql+" $1::"+rhs+" ESCAPE E'\\\\' AS v", !op.negative, `a\_b`))
					}
				})
			})
		}
	}
}
