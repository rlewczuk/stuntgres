package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreDiagnostics(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_errors(a int4range,m int4multirange,t text,b boolean,n numeric)", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"multirange", "unnest"} {
			for _, input := range []struct{ expr, typ string }{{"1", "integer"}, {"t", "text"}, {"b", "boolean"}, {"n", "numeric"}} {
				q := "SELECT /*é*/ " + name + "(" + input.expr + ") FROM more_errors"
				s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42883", "function "+name+"("+input.typ+") does not exist", hint, name, "", ""))
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s.Prepared, s.ErrorStage = true, "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			q := "SELECT " + name + "(missing) FROM more_errors"
			s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42703", `column "missing" does not exist`, "", "missing", "", ""))
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s.Prepared, s.ErrorStage = true, "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, join := range []string{"RIGHT", "FULL"} {
			q := "SELECT * FROM more_errors s " + join + " JOIN LATERAL unnest(s.m) u(r) ON true"
			want := inspectDiagnostic(q, "42P10", `invalid reference to FROM-clause entry for table "s"`, "", "s.m", "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", "")
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}

