package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterLengthLockPrecedence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"review_length_stranger"})
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		enumAlterExec(t, f, f.A, "CREATE TYPE review_length AS ENUM ('old','q')")
		enumAlterExec(t, f, f.A, "CREATE ROLE review_length_stranger")
		if pg {
			enumAlterExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO review_length_stranger")
		}
		t.Cleanup(func() { f.B.ExecContext(f.Pair.Ctx, "RESET SESSION AUTHORIZATION") })
		enumAlterExec(t, f, f.A, "BEGIN")
		t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
		enumAlterExec(t, f, f.A, "ALTER TYPE review_length ADD VALUE 'held'")
		enumAlterExec(t, f, f.B, "SET lock_timeout='150ms'")
		for _, label := range []string{strings.Repeat("x", 64), strings.Repeat("é", 32), strings.Repeat("x", 64) + "\n"} {
			want := enumAlterDiagnostic("42602", "invalid enum label \""+label+"\"")
			want.Detail = "Labels must be 63 bytes or less."
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"ALTER TYPE review_length ADD VALUE " + enumAlterReviewLiteral(label), "ALTER TYPE review_length ADD VALUE IF NOT EXISTS " + enumAlterReviewLiteral(label) + " BEFORE 'absent'", "ALTER TYPE review_length RENAME VALUE 'q' TO " + enumAlterReviewLiteral(label), "ALTER TYPE review_length RENAME VALUE 'absent' TO " + enumAlterReviewLiteral(label)} {
					enumAlterError(t, f, f.B, q, prepared, false, "execute", want)
				}
			}
		}
		longSQL := enumAlterReviewLiteral(strings.Repeat("x", 64))
		for _, prepared := range []bool{false, true} {
			enumAlterError(t, f, f.B, "ALTER TYPE absent_review_type ADD VALUE "+longSQL, prepared, false, "execute", enumAlterDiagnostic("42704", `type "absent_review_type" does not exist`))
			enumAlterError(t, f, f.B, "ALTER TYPE int4 ADD VALUE "+longSQL, prepared, false, "execute", enumAlterDiagnostic("42809", "integer is not an enum"))
		}
		enumAlterExec(t, f, f.B, "SET SESSION AUTHORIZATION review_length_stranger")
		for _, prepared := range []bool{false, true} {
			enumAlterError(t, f, f.B, "ALTER TYPE review_length ADD VALUE "+longSQL, prepared, false, "execute", enumAlterDiagnostic("42501", "must be owner of type review_length"))
		}
		enumAlterExec(t, f, f.B, "RESET SESSION AUTHORIZATION")
		// Valid SQL proves A really holds this type resource through the errors.
		enumAlterError(t, f, f.B, "ALTER TYPE review_length ADD VALUE 'would_wait'", false, false, "execute", enumAlterDiagnostic("55P03", "canceling statement due to lock timeout"))
		enumAlterError(t, f, f.B, "ALTER TYPE review_length RENAME VALUE 'q' TO 'would_rename'", true, false, "execute", enumAlterDiagnostic("55P03", "canceling statement due to lock timeout"))
		enumAlterExec(t, f, f.A, "ROLLBACK")
		fixture.RunStep(t, f.Pair.Ctx, f.B, f.Recorder, fixture.Select("SELECT enum_range(NULL::review_length)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{old,q}"}}))
		enumAlterExec(t, f, f.B, "ALTER TYPE review_length ADD VALUE 'after_release'")
		fixture.RunStep(t, f.Pair.Ctx, f.B, f.Recorder, fixture.Select("SELECT enum_range(NULL::review_length)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{old,q,after_release}"}}))
	})
}
