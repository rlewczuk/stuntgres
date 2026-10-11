package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterSpecialLabelDiagnostics(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		enumAlterExec(t, f, f.A, "CREATE TYPE review_escaped AS ENUM ('old')")
		held, err := f.A.PrepareContext(f.Pair.Ctx, "SELECT $1::review_escaped")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
		for _, label := range []string{`back\slash`, `double"quote`, "line\nbreak", "tab\tlabel", "carriage\rreturn", "control\x01label"} {
			quoted := enumAlterReviewLiteral(label)
			enumAlterExec(t, f, f.A, "ALTER TYPE review_escaped ADD VALUE "+quoted)
			missing := "absent_" + label
			missingSQL := enumAlterReviewLiteral(missing)
			long := strings.Repeat("x", 64) + label
			for _, prepared := range []bool{false, true} {
				duplicate := enumAlterDiagnostic("42710", "enum label \""+label+"\" already exists")
				for _, q := range []string{"ALTER TYPE review_escaped ADD VALUE " + quoted, "ALTER TYPE review_escaped RENAME VALUE 'old' TO " + quoted, "ALTER TYPE review_escaped RENAME VALUE " + quoted + " TO " + quoted} {
					enumAlterError(t, f, f.A, q, prepared, false, "execute", duplicate)
				}
				*f.Notices = nil
				s := fixture.Exec("ALTER TYPE review_escaped ADD VALUE IF NOT EXISTS "+quoted+" BEFORE "+missingSQL, 0)
				s.Prepared = prepared
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
				notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: "enum label \"" + label + "\" already exists, skipping"}
				if len(*f.Notices) != 1 || (*f.Notices)[0] != notice {
					t.Errorf("raw label notice got %#v want %#v", *f.Notices, notice)
				}
				absent := enumAlterDiagnostic("22023", "\""+missing+"\" is not an existing enum label")
				for _, q := range []string{"ALTER TYPE review_escaped RENAME VALUE " + missingSQL + " TO " + quoted, "ALTER TYPE review_escaped ADD VALUE 'not_added' BEFORE " + missingSQL, "ALTER TYPE review_escaped ADD VALUE 'not_added' AFTER " + missingSQL} {
					enumAlterError(t, f, f.A, q, prepared, false, "execute", absent)
				}
				overlong := enumAlterDiagnostic("42602", "invalid enum label \""+long+"\"")
				overlong.Detail = "Labels must be 63 bytes or less."
				for _, q := range []string{"ALTER TYPE review_escaped ADD VALUE " + enumAlterReviewLiteral(long), "ALTER TYPE review_escaped RENAME VALUE 'old' TO " + enumAlterReviewLiteral(long)} {
					enumAlterError(t, f, f.A, q, prepared, false, "execute", overlong)
				}
				invalid := enumAlterDiagnostic("22P02", "invalid input value for enum review_escaped: \""+missing+"\"")
				invalid.Position = "8"
				phase := "execute"
				if prepared {
					phase = "prepare"
				}
				enumAlterError(t, f, f.A, "SELECT "+missingSQL+"::review_escaped", prepared, true, phase, invalid)
				invalid.Position = ""
				invalid.Context = "unnamed portal parameter $1 = '...'"
				enumAlterError(t, f, f.A, "SELECT $1::review_escaped", prepared, true, "execute", invalid, missing)
			}
			unsafeLabel := "pending_" + label
			unsafeSQL := enumAlterReviewLiteral(unsafeLabel)
			enumAlterExec(t, f, f.A, "BEGIN")
			enumAlterExec(t, f, f.A, "ALTER TYPE review_escaped ADD VALUE "+unsafeSQL)
			for _, prepared := range []bool{false, true} {
				for _, kind := range []string{"literal", "bound", "endpoint", "range"} {
					enumAlterExec(t, f, f.A, "SAVEPOINT raw_producer")
					want := enumAlterReviewUnsafe(unsafeLabel, "review_escaped")
					q := "SELECT enum_last(NULL::review_escaped)"
					phase := "execute"
					var args []any
					if kind == "literal" {
						q = "SELECT " + unsafeSQL + "::review_escaped"
						want.Position = strconv.Itoa(len([]rune("SELECT ")) + 1)
						if prepared {
							phase = "prepare"
						}
					}
					if kind == "bound" {
						q = "SELECT $1::review_escaped"
						args = []any{unsafeLabel}
						want.Context = "unnamed portal parameter $1 = '...'"
					}
					if kind == "range" {
						q = "SELECT enum_range(NULL::review_escaped)"
					}
					enumAlterError(t, f, f.A, q, prepared, true, phase, want, args...)
					enumAlterExec(t, f, f.A, "ROLLBACK TO raw_producer")
					enumAlterExec(t, f, f.A, "RELEASE raw_producer")
				}
			}
			enumAlterExec(t, f, f.A, "SAVEPOINT held_raw")
			rows, err := held.QueryContext(f.Pair.Ctx, unsafeLabel)
			if rows != nil {
				rows.Close()
				t.Error("unsafe held raw bind exposed rows")
			}
			wantHeld := enumAlterReviewUnsafe(unsafeLabel, "review_escaped")
			wantHeld.Context = "unnamed portal parameter $1 = '...'"
			fixture.EnumReferenceAssertDiagnostic(t, err, wantHeld)
			enumAlterExec(t, f, f.A, "ROLLBACK TO held_raw")
			enumAlterExec(t, f, f.A, "RELEASE held_raw")
			enumAlterExec(t, f, f.A, "ROLLBACK")
			want := enumAlterDiagnostic("22P02", "invalid input value for enum review_escaped: \""+unsafeLabel+"\"")
			want.Position = "8"
			enumAlterError(t, f, f.A, "SELECT "+unsafeSQL+"::review_escaped", false, true, "execute", want)
		}
	})
}
