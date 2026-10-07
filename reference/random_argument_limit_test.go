//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceRandomArgumentLimit(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE limits(id INT)", 0))
	arguments := make([]string, 101)
	for i := range arguments {
		arguments[i] = "1"
	}
	for _, name := range []string{"random", "random_normal", "setseed"} {
		for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
			q := prefix + name + "(" + strings.Join(arguments, ",") + ")"
			pos := "8"
			if prefix != "SELECT " {
				pos = "14"
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: pos})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				p.step(t, s)
			}
		}
	}
	arguments[0] = "missing"
	q := "SELECT random(" + strings.Join(arguments, ",") + ") FROM limits"
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "15"})
		s.selectRows = true
		s.prepared = prepared
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		p.step(t, s)
	}
}
