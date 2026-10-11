package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent source-derived expectations confirmed by root's pinned18 review
// capture before production remedies, not learned from native outputs.
// Semantic utility Prepare must succeed before the exact Execute record.
func characterReviewModifierCases() []struct {
	decl, typ, value string
	diagnostic       fixture.Diagnostic
} {
	text := fixture.Diagnostic{Code: "42601", Message: `type modifier is not allowed for type "text"`, Position: "31"}
	identifier := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "foo"`, Position: "31"}
	return []struct {
		decl, typ, value string
		diagnostic       fixture.Diagnostic
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
		{`bpchar("2")`, "BPCHAR", "a ", fixture.Diagnostic{}},
		{`"bpchar"("2")`, "BPCHAR", "a ", fixture.Diagnostic{}},
		{`"varchar"("2")`, "VARCHAR", "a", fixture.Diagnostic{}},
	}
}

func TestReferenceCharacterReviewModifierDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range characterReviewModifierCases() {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.decl+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				q := "/*é🙂*/ CREATE TABLE invalid(v " + tc.decl + ")"
				if tc.diagnostic.Code != "" {
					s := fixture.ImplicitError(q, tc.diagnostic)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					p.Step(t, s)
					missing := fixture.ImplicitError("SELECT v FROM invalid", fixture.Diagnostic{Code: "42P01", Message: `relation "invalid" does not exist`, Position: "15"})
					missing.SelectRows, missing.ErrorStage = true, "execute"
					p.Step(t, missing)
					return
				}
				s := fixture.Exec(q, 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				p.Step(t, s)
				for _, backend := range p.Backends() {
					fixture.CharacterMetadata(t, p, backend.DB, "SELECT v FROM invalid", prepared, !backend.PG, []string{"v"}, []string{tc.typ}, []int64{2}, []fixture.CharacterNullability{{true, true}})
				}
				p.Step(t, fixture.Exec("INSERT INTO invalid VALUES('a')", 1))
				p.Step(t, fixture.Select("SELECT v FROM invalid", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
				p.Step(t, fixture.Exec("DROP TABLE invalid", 0))
			})
		}
	}
}
