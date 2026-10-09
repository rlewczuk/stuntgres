package reference_test

import (
	"strconv"
	"testing"
)

// Independent source-derived expectations confirmed by root's pinned18 review
// capture before production remedies, not learned from native outputs.
// Semantic utility Prepare must succeed before the exact Execute record.
func characterReviewModifierCases() []struct {
	decl, typ, value string
	diagnostic       referenceDiagnostic
} {
	text := referenceDiagnostic{Code: "42601", Message: `type modifier is not allowed for type "text"`, Position: "31"}
	identifier := referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "foo"`, Position: "31"}
	return []struct {
		decl, typ, value string
		diagnostic       referenceDiagnostic
	}{
		{`text('oops')`, "", "", text},
		{`text(2147483648)`, "", "", text},
		{`text(+2)`, "", "", text},
		{`"text"('oops')`, "", "", text},
		{`"text"(2147483648)`, "", "", text},
		{`"text"(+2)`, "", "", text},
		{`bpchar(foo)`, "", "", identifier},
		{`"bpchar"(foo)`, "", "", identifier},
		{`"varchar"(foo)`, "", "", identifier},
		{`bpchar("2")`, "BPCHAR", "a ", referenceDiagnostic{}},
		{`"bpchar"("2")`, "BPCHAR", "a ", referenceDiagnostic{}},
		{`"varchar"("2")`, "VARCHAR", "a", referenceDiagnostic{}},
	}
}

func TestReferenceCharacterReviewModifierDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range characterReviewModifierCases() {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.decl+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				q := "/*é🙂*/ CREATE TABLE invalid(v " + tc.decl + ")"
				if tc.diagnostic.Code != "" {
					s := referenceImplicitError(q, tc.diagnostic)
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					p.step(t, s)
					missing := referenceImplicitError("SELECT v FROM invalid", referenceDiagnostic{Code: "42P01", Message: `relation "invalid" does not exist`, Position: "15"})
					missing.selectRows, missing.errorStage = true, "execute"
					p.step(t, missing)
					return
				}
				s := referenceExec(q, 0)
				if prepared {
					s = referencePrepared(s)
				}
				p.step(t, s)
				for _, backend := range p.backends() {
					referenceCharacterMetadata(t, p, backend.db, "SELECT v FROM invalid", prepared, !backend.pg, []string{"v"}, []string{tc.typ}, []int64{2}, []referenceCharacterNullability{{true, true}})
				}
				p.step(t, referenceExec("INSERT INTO invalid VALUES('a')", 1))
				p.step(t, referenceSelect("SELECT v FROM invalid", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
				p.step(t, referenceExec("DROP TABLE invalid", 0))
			})
		}
	}
}
