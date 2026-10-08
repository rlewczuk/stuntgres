//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func stringCoreKelvinReference(t *testing.T, p *referencePair, oracle bool) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
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
						s := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: "invalid normalization form: " + form}, args...)
						if prepared {
							s = referencePrepared(s)
							s.errorStage = "execute"
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
				s := referenceSelect("SELECT "+function+"($1,$2) AS v", []string{"v"}, []string{typ}, [][]any{{want}}, tc.source, tc.form)
				if prepared {
					s = referencePrepared(s)
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
				s := referenceSelect("SELECT "+function+"($1,$2) AS v", []string{"v"}, []string{typ}, [][]any{{nil}}, args...)
				if prepared {
					s = referencePrepared(s)
				}
				run(t, s)
			}
		}
	}
}

func TestReferenceStringCoreReviewOracle(t *testing.T) {
	p := newReferencePair(t)
	t.Run("forms", func(t *testing.T) { stringCoreKelvinReference(t, p, true) })
	t.Run("deparse", func(t *testing.T) { stringCoreReviewDeparseReference(t, p, true) })
	t.Run("resolution", func(t *testing.T) { stringCoreReviewResolutionReference(t, p, true) })
	t.Run("binary_cutover", func(t *testing.T) { stringCoreReviewBinaryConsumptionReference(t, p, true) })
	t.Run("RestoreSQL", func(t *testing.T) { stringCoreReviewRestoreSQLReference(t, p, true) })
}

func TestReferenceStringCoreReviewForms(t *testing.T) {
	stringCoreKelvinReference(t, newReferencePair(t), false)
}
