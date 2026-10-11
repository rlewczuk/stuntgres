package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func stringCoreKelvinReference(t *testing.T, p *fixture.Pair) {
	run := func(t *testing.T, s fixture.Step) {
		t.Helper()
		p.Step(t, s)
	}
	for _, function := range []string{"pg_catalog.normalize", "pg_catalog.is_normalized"} {
		for _, form := range []string{"NFKC", "NFKD"} {
			for _, parameters := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/parameters=%t/prepared=%t", function, form, parameters, prepared), func(t *testing.T) {
						q := "SELECT " + function + "('x','" + form + "')"
						var args []any
						if parameters {
							q = "SELECT " + function + "($1,$2)"
							args = []any{"x", form}
						}
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: " + form}, args...)
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "execute"
						}
						run(t, s)
					})
				}
			}
		}
		for _, tc := range []struct {
			source, form string
			want         any
		}{{"é", "nfKc", "é"}, {"é", "NfKd", "é"}} {
			want, typ := tc.want, "TEXT"
			if function == "pg_catalog.is_normalized" {
				want, typ = false, "BOOL"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT "+function+"($1,$2) AS v", []string{"v"}, []string{typ}, [][]any{{want}}, tc.source, tc.form)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(t, s)
			}
		}
		for _, args := range [][]any{{nil, "NFKC"}, {"x", nil}} {
			typ := "TEXT"
			if function == "pg_catalog.is_normalized" {
				typ = "BOOL"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT "+function+"($1,$2) AS v", []string{"v"}, []string{typ}, [][]any{{nil}}, args...)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(t, s)
			}
		}
	}
}

func TestReferenceStringCoreReviewForms(t *testing.T) {
	stringCoreKelvinReference(t, fixture.NewScenario(t))
}
