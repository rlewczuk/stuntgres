package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func regroleError(q, code, message, position, portal string, args ...any) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position, want.Context = position, portal
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.SelectRows = true
	return s
}

func TestReferenceRegroleDiagnostics(t *testing.T) {
	regroleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT $1::regrole"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ input, code, message string }{
			{"rr_absent", "42704", `role "rr_absent" does not exist`},
			{"MiSsInG", "42704", `role "missing" does not exist`},
			{`"MiSsInG"`, "42704", `role "MiSsInG" does not exist`},
			{"foo.bar", "42602", "invalid name syntax"}, {"", "42602", "invalid name syntax"},
			{`"s`, "42602", "invalid name syntax"}, {`"s"tail`, "42602", "invalid name syntax"},
			{`""`, "42704", `role "" does not exist`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
			{"08", "22P02", `invalid input syntax for type oid: "08"`},
			{"-1", "42704", `role "-1" does not exist`}, {" 10 ", "42704", `role "10" does not exist`},
			{"0x10", "42704", `role "0x10" does not exist`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				// A numeric-looking role exists: padded input takes the symbolic route.
				if tc.input == " 10 " {
					regroleBoth(t, p, b, c, fixture.Select("SELECT ' 10 '::regrole AS r", []string{"r"}, []string{"REGROLE"}, [][]any{{`"10"`}}))
					return
				}
				literal := "SELECT '" + strings.ReplaceAll(tc.input, "'", "''") + "'::regrole"
				s := regroleError(literal, tc.code, tc.message, "8", "")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				portal := "unnamed portal parameter $1 = '...'"
				if tc.input == "" {
					portal = "unnamed portal parameter $1 = ''"
				}
				s = regroleError(q, tc.code, tc.message, "", portal, tc.input)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regroleError("SELECT 'é' AS prefix, 'rr_absent'::regrole", "42704", `role "rr_absent" does not exist`, "23", ""))
		for _, q := range []string{"SELECT $1::text::regrole", "SELECT (-1)::bigint::regrole", "SELECT 4294967296::bigint::regrole"} {
			t.Run(q, func(t *testing.T) {
				var args []any
				if strings.Contains(q, "$1") {
					args = []any{"rr_absent"}
				}
				code, message := "22003", "OID out of range"
				if len(args) != 0 {
					code, message = "42704", `role "rr_absent" does not exist`
				}
				regroleBoth(t, p, b, c, regroleError(q, code, message, "", "", args...))
			})
		}
	})
}
