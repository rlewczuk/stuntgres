package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func citextPatternHeldRegex(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, op, rhs string, negative bool) {
	t.Helper()
	q := "SELECT $1::citext " + op + " $2::" + rhs + " AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		s, p any
		want any
		bad  bool
	}{
		{"ABC", "^abc$", !negative, false}, {"ABC", "^abd$", negative, false},
		{"ABC", "(?c)^abc$", negative, false}, {"ABC", "(?i)^abc$", !negative, false},
		{nil, "[", nil, false}, {"ABC", nil, nil, false}, {"ABC", "[", nil, true},
		{nil, "[", nil, false}, {"ABC", "^abc$", !negative, false},
	} {
		s := citextPatternBool(q, tc.want, tc.s, tc.p)
		if tc.bad {
			s = fixture.ErrorDiagnostic(q, citextPatternRegexDiagnostic("brackets [] not balanced"), tc.s, tc.p)
			s.SelectRows = true
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferenceCitextPatternPrepared(t *testing.T) {
	for _, rhs := range []string{"citext", "text"} {
		for _, op := range citextPatternOps[:4] {
			t.Run(rhs+"/"+op.sql, func(t *testing.T) {
				citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					citextInstall(t, p, b, c)
					for _, flag := range []string{"c", "i"} {
						want := (flag == "i") != op.negative
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'ABC'::citext "+op.sql+" '(?"+flag+")^abc$'::"+rhs+" AS v", want))
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext "+op.sql+" $2::"+rhs+" AS v", want, "ABC", "(?"+flag+")^abc$"))
					}
					citextPatternHeldRegex(t, p, b, c, op.sql, rhs, op.negative)
				})
			})
		}
		for _, escape := range []string{"citext", "text"} {
			t.Run(rhs+"/escape/"+escape, func(t *testing.T) {
				citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					citextInstall(t, p, b, c)
					q := "SELECT $1::citext LIKE $2::" + rhs + " ESCAPE $3::" + escape + " AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, tc := range []struct {
						s, p, e any
						want    any
						bad     bool
					}{
						{"A%B", "a!%b", "!", true, false}, {"A%B", "a!%b", "!!", nil, true},
						{"A%B", nil, "!", nil, false}, {"A%B", "a!%b", nil, nil, false},
						{"A%B", "a!%b", "!", true, false},
					} {
						s := citextPatternBool(q, tc.want, tc.s, tc.p, tc.e)
						if tc.bad {
							d := fixture.ControlsReferenceDiagnostic("22025", "invalid escape string")
							d.Hint = "Escape string must be empty or one character."
							s = fixture.ErrorDiagnostic(q, d, tc.s, tc.p, tc.e)
							s.SelectRows = true
							s.ErrorStage = "execute"
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				})
			})
		}
	}
}
