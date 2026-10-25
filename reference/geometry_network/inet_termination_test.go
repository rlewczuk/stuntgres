package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETIPv4Termination(t *testing.T) {
	for i, tc := range []struct{ typ, input, output string }{
		{"inet", "1.2.3.4.", "1.2.3.4"},
		{"inet", "1.2.3./24", "1.2.3.0/24"},
		{"inet", "1.2.3.4./32", "1.2.3.4"},
		{"inet", "1.2./16", "1.2.0.0/16"},
		{"inet", "1./8", "1.0.0.0/8"},
		{"inet", "1.2.3.", ""},
		{"inet", "1.2.3./32", ""},
		{"inet", "1.2.3.4..", ""},
		{"inet", "1.2.3../24", ""},
		{"cidr", "1.2.3.4.", ""},
		{"cidr", "1.2.3./24", ""},
		{"inet", "::ffff:1.2.3.4.", ""},
		{"cidr", "::ffff:1.2.3.4.", ""},
	} {
		t.Run(fmt.Sprintf("%02d_%s", i, tc.typ), func(t *testing.T) {
			inetTerminationCase(t, tc.typ, tc.input, tc.output)
		})
	}
}

func inetTerminationCase(t *testing.T, typeName, input, output string) {
	t.Helper()
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for _, b := range backends {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE n(v "+typeName+")", 0))
		typ := "INET"
		if typeName == "cidr" {
			typ = "CIDR"
		}
		for _, prepared := range []bool{false, true} {
			if output != "" {
				for _, s := range []fixture.Step{
					fixture.Exec("INSERT INTO n VALUES($1)", 1, input),
					fixture.Exec("INSERT INTO n VALUES('"+input+"')", 1),
					fixture.Select("SELECT '"+input+"'::"+typeName+" AS v", []string{"v"}, []string{typ}, [][]any{{output}}),
				} {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			} else {
				d := inetDiagnostic(typeName, input, false)
				d.Context = "unnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic("INSERT INTO n VALUES($1)", d, input)
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				d.Context = ""
				for _, literal := range []struct{ q, position string }{
					{"SELECT '" + input + "'::" + typeName, "8"},
					{"INSERT INTO n VALUES('" + input + "')", "22"},
				} {
					d.Position = literal.position
					s = fixture.ErrorDiagnostic(literal.q, d)
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		var expected [][]any
		if output != "" {
			expected = [][]any{{output}, {output}, {output}, {output}}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{typ}, expected))
		// Invalid inputs must not poison the connection or subsequent valid input.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO n VALUES($1)", 1, "::1"))
		control := "::1"
		if typeName == "cidr" {
			control += "/128"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM n WHERE v = '::1'", []string{"v"}, []string{typ}, [][]any{{control}}))
	}
}
