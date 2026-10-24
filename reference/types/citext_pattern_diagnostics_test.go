package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextPatternDiagnostics(t *testing.T) {
	for _, rhs := range []string{"citext", "text"} {
		for _, op := range citextPatternOps {
			cases := []struct{ pattern, message string }{{"[", "brackets [] not balanced"}, {"(", "parentheses () not balanced"}, {"(?z)a", "invalid embedded option"}}
			if !op.regex {
				cases = []struct{ pattern, message string }{{"\\", "LIKE pattern must not end with escape character"}}
			}
			for _, tc := range cases {
				for _, literal := range []bool{false, true} {
					name := rhs + "/" + op.sql + "/" + tc.pattern + "/bound"
					if literal {
						name += "/literal"
					}
					t.Run(name, func(t *testing.T) {
						citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							citextInstall(t, p, b, c)
							q := "SELECT /* é🙂 */ 'x'::citext " + op.sql + " $1::" + rhs + " AS v"
							args := []any{tc.pattern}
							if literal {
								q = "SELECT /* é🙂 */ 'x'::citext " + op.sql + " $pattern$" + tc.pattern + "$pattern$::" + rhs + " AS v"
								args = nil
							}
							d := citextPatternRegexDiagnostic(tc.message)
							if !op.regex {
								d = fixture.ControlsReferenceDiagnostic("22025", tc.message)
							}
							for _, prepared := range []bool{false, true} {
								citextError(t, p, b, c, q, prepared, true, false, d, args...)
							}
						})
					})
				}
			}
		}
		for _, escape := range []string{"citext", "text"} {
			for _, op := range citextPatternOps[4:] {
				for _, literal := range []bool{false, true} {
					name := rhs + "/" + escape + "/" + op.sql + "/invalid_escape/bound"
					if literal {
						name += "/literal"
					}
					t.Run(name, func(t *testing.T) {
						citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							citextInstall(t, p, b, c)
							q := "SELECT 'ABC'::citext " + op.sql + " 'abc'::" + rhs + " ESCAPE $1::" + escape + " AS v"
							args := []any{"!!"}
							if literal {
								q = "SELECT 'ABC'::citext " + op.sql + " 'abc'::" + rhs + " ESCAPE '!!'::" + escape + " AS v"
								args = nil
							}
							d := fixture.ControlsReferenceDiagnostic("22025", "invalid escape string")
							d.Hint = "Escape string must be empty or one character."
							for _, prepared := range []bool{false, true} {
								citextError(t, p, b, c, q, prepared, true, false, d, args...)
							}
						})
					})
				}
			}
		}
	}
}
