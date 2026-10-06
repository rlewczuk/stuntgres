//go:build reference

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

// This entrypoint executes SQL against PostgreSQL only, using the configured
// pinned fixture rather than a second harness or inferred native parity.
func TestReferenceCharacterReviewModifierOracle(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range characterReviewModifierCases() {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.decl+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				q := "/*é🙂*/ CREATE TABLE invalid(v " + tc.decl + ")"
				characterOracleExec(t, p, q, prepared, false, tc.diagnostic)
				if tc.diagnostic.Code != "" {
					return
				}
				t.Logf("query=%q prepared=%t stage=execute accepted length=2", q, prepared)
				referenceCharacterMetadata(t, p, p.postgres, "SELECT v FROM invalid", prepared, false, true, []string{"v"}, []string{tc.typ}, []int64{2})
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO invalid VALUES('a')", 1))
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT v FROM invalid", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DROP TABLE invalid", 0))
			})
		}
	}
}

func TestReferenceCharacterReviewModifierDiagnostics(t *testing.T) {
	p := newReferencePair(t)
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
					return
				}
				s := referenceExec(q, 0)
				if prepared {
					s = referencePrepared(s)
				}
				p.step(t, s)
				referenceCharacterMetadata(t, p, p.memory, "SELECT v FROM invalid", prepared, true, true, []string{"v"}, []string{tc.typ}, []int64{2})
				referenceCharacterMetadata(t, p, p.postgres, "SELECT v FROM invalid", prepared, false, true, []string{"v"}, []string{tc.typ}, []int64{2})
				p.step(t, referenceExec("INSERT INTO invalid VALUES('a')", 1))
				p.step(t, referenceSelect("SELECT v FROM invalid", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
				p.step(t, referenceExec("DROP TABLE invalid", 0))
			})
		}
	}
}
