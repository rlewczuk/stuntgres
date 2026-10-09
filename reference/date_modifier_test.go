package reference_test

import "testing"

func TestReferenceDateModifierDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, typ := range []string{"date(0)", "date(3)", `"date"(3)`} {
				want := referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20"}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("CREATE TABLE bad(v "+typ+")", want)
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
				run(referenceExec("CREATE TABLE bad(v DATE)", 0))
				run(referenceExec("DROP TABLE bad", 0))
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT DATE(3) '1999-01-08' FROM d", referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8"})
				s.prepared = prepared
				s.selectRows = true
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
			}
		})
	}
}

func TestReferenceDateDecodeDiagnosticsAndModifierPhases(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			for _, tc := range []struct{ input, code, primary string }{
				{"1999-01-08 25:00", "22008", "date/time field value out of range"},
				{"1999-01-08 24:00:00.1", "22008", "date/time field value out of range"},
				{"J2451187.5 J2451187.5", "22007", "invalid input syntax for type date"},
				{"J2451187.5 1999-01-08", "22007", "invalid input syntax for type date"},
				{"J-1", "22007", "invalid input syntax for type date"},
				{"J2147483647", "22008", "date out of range"},
				{"999999999999999999-01-01", "22008", "date/time field value out of range"},
				{"1999-01-08 04:05 UTC UTC", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05 EDT DST", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05:06 now1", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05:06 now+2", "22007", "invalid input syntax for type date"},
				{"1999-01-08 04:05+16", "22009", "time zone displacement out of range"},
				{"1999-01-08 04:05+01:99", "22009", "time zone displacement out of range"},
				{"2001-02-29 25:00", "22008", "date/time field value out of range"},
				{"5874898-01-01 25:00", "22008", "date/time field value out of range"},
				{"2001-13-01 04:05+16", "22009", "time zone displacement out of range"},
				{"1999-01-08 04:05 America/Bogus", "22023", ""},
			} {
				message := tc.primary + ": \"" + tc.input + "\""
				if tc.primary == "" {
					message = `time zone "america/bogus" not recognized`
				}
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Code: tc.code, Message: message, Position: "13"}
					s := referenceImplicitError("SELECT DATE '"+tc.input+"' FROM d", want)
					s.prepared = prepared
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					want.Position = ""
					want.Context = "unnamed portal parameter $1 = '...'"
					s = referenceImplicitError("INSERT INTO d VALUES($1)", want)
					s.args = []any{tc.input}
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
		})
	}
}
