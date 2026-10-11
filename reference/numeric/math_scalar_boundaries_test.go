//go:build reference

package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarSpecialsAndLimits(t *testing.T) {
	p := fixture.NewPair(t)
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp"} {
		for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
			want := special
			if name == "abs" && special == "-Infinity" {
				want = "Infinity"
			}
			if name == "exp" && special == "-Infinity" {
				want = "0"
			}
			s := fixture.Select("SELECT "+name+"(COALESCE('"+special+"',1.0))", []string{name}, []string{"NUMERIC"}, [][]any{{want}})
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
		}
	}
	for _, a := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
		for _, b := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
			q := "SELECT div(COALESCE('" + a + "',1.0),COALESCE('" + b + "',1.0))"
			if b == "0" && a != "NaN" {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				p.Step(t, s)
				p.Step(t, fixture.Prepared(s))
				continue
			}
			want := "0"
			if a == "NaN" || b == "NaN" || strings.Contains(a, "Infinity") && strings.Contains(b, "Infinity") {
				want = "NaN"
			} else if strings.Contains(a, "Infinity") {
				want = "Infinity"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-Infinity"
				}
			} else if !strings.Contains(b, "Infinity") && a != "0" {
				want = "1"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-1"
				}
			}
			s := fixture.Select(q, []string{"div"}, []string{"NUMERIC"}, [][]any{{want}})
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
		}
	}
	for _, tc := range []struct{ name, input string }{{"ceil", strings.Repeat("9", 131072) + ".1"}, {"ceiling", strings.Repeat("9", 131072) + ".1"}, {"floor", "-" + strings.Repeat("9", 131072) + ".1"}} {
		s := fixture.ImplicitError("SELECT "+tc.name+"("+tc.input+")", fixture.Diagnostic{Code: "22003", Message: "value overflows numeric format"})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ name, input, want string }{{"abs", "-1e131071", "1" + strings.Repeat("0", 131071)}, {"floor", strings.Repeat("9", 131072) + ".1", strings.Repeat("9", 131072)}, {"div", "1e131071,1", "1" + strings.Repeat("0", 131071)}} {
		s := fixture.Select("SELECT "+tc.name+"("+tc.input+")", []string{tc.name}, []string{"NUMERIC"}, [][]any{{tc.want}})
		p.Step(t, s)
	}
}
