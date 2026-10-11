package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomArgumentLimit(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE limits(id INT)", 0))
	arguments := make([]string, 101)
	for i := range arguments {
		arguments[i] = "1"
	}
	for _, name := range []string{"random", "random_normal", "setseed"} {
		// Independently require signature diagnostics at the allowed boundary.
		q := "SELECT " + name + "(" + strings.Join(arguments[:100], ",") + ")"
		want := fixture.Diagnostic{
			Code:     "42883",
			Message:  "function " + name + "(" + strings.Repeat("integer, ", 99) + "integer) does not exist",
			Hint:     "No function matches the given name and argument types. You might need to add explicit type casts.",
			Position: "8",
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, want)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
			if !prepared {
				s.SelectRows = false
				p.Step(t, s)
			}
		}
		for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
			q := prefix + name + "(" + strings.Join(arguments, ",") + ")"
			pos := "8"
			if prefix != "SELECT " {
				pos = "14"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: pos})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				p.Step(t, s)
				if !prepared {
					s.SelectRows = false
					p.Step(t, s)
				}
			}
		}
	}
	arguments[0] = "missing"
	q := "SELECT random(" + strings.Join(arguments, ",") + ") FROM limits"
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "15"})
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		p.Step(t, s)
		if !prepared {
			s.SelectRows = false
			p.Step(t, s)
		}
	}
}
