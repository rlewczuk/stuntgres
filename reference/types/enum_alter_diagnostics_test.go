package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_stranger", "alter_member", "alter_owner"})
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{"CREATE TYPE mood AS ENUM ('z','q')", "CREATE DOMAIN alter_domain AS mood", "CREATE TYPE alter_composite AS (v integer)", "CREATE USER alter_stranger"} {
			enumAlterExec(t, f, f.A, q)
		}
		cases := []struct{ q, code, message, detail string }{
			{"ALTER TYPE mood ADD VALUE 'q'", "42710", `enum label "q" already exists`, ""},
			{"ALTER TYPE mood ADD VALUE 'a' BEFORE 'absent'", "22023", `"absent" is not an existing enum label`, ""},
			{"ALTER TYPE mood ADD VALUE 'a' AFTER 'absent'", "22023", `"absent" is not an existing enum label`, ""},
			{"ALTER TYPE mood RENAME VALUE 'absent' TO 'q'", "22023", `"absent" is not an existing enum label`, ""},
			{"ALTER TYPE mood RENAME VALUE 'z' TO 'q'", "42710", `enum label "q" already exists`, ""},
			{"ALTER TYPE mood RENAME VALUE 'q' TO 'q'", "42710", `enum label "q" already exists`, ""},
			{"ALTER TYPE integer ADD VALUE 'a'", "42704", `type "integer" does not exist`, ""},
			{"ALTER TYPE int4 ADD VALUE 'a'", "42809", "integer is not an enum", ""},
			{"ALTER TYPE alter_domain ADD VALUE 'a'", "42809", "alter_domain is not an enum", ""},
			{"ALTER TYPE alter_composite RENAME VALUE 'a' TO 'b'", "42809", "alter_composite is not an enum", ""},
		}
		for _, label := range []string{strings.Repeat("x", 64), strings.Repeat("é", 32)} {
			for _, q := range []string{"ALTER TYPE mood ADD VALUE '" + label + "' BEFORE 'absent'", "ALTER TYPE mood RENAME VALUE 'absent' TO '" + label + "'"} {
				cases = append(cases, struct{ q, code, message, detail string }{q, "42602", `invalid enum label "` + label + `"`, "Labels must be 63 bytes or less."})
			}
		}
		for i, tc := range cases {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				want := enumAlterDiagnostic(tc.code, tc.message)
				want.Detail = tc.detail
				for _, prepared := range []bool{false, true} {
					enumAlterError(t, f, f.A, tc.q, prepared, false, "execute", want)
				}
			})
		}
		for _, q := range []string{"ALTER TYPE absent ADD VALUE 'a'", "ALTER TYPE missing.mood ADD VALUE 'a'"} {
			want := enumAlterDiagnostic("42704", `type "absent" does not exist`)
			if strings.Contains(q, "missing.") {
				want = enumAlterDiagnostic("3F000", `schema "missing" does not exist`)
			}
			for _, prepared := range []bool{false, true} {
				enumAlterError(t, f, f.A, q, prepared, false, "execute", want)
			}
		}
		for _, q := range []string{"ALTER TYPE mood ADD VALUE q", "ALTER TYPE mood ADD VALUE $1", "ALTER TYPE mood RENAME VALUE 'q' 'end'", "ALTER TYPE mood ADD VALUE 'a' nonsense", "ALTER TYPE /*é*/ mood ADD VALUE 'a' BEFORE", "ALTER TYPE mood RENAME VALUE IF NOT EXISTS 'q' TO 'end'"} {
			// Independently derived character positions and parser wording are
			// confirmed against the pinned PostgreSQL18 discovery.
			token := "q"
			pos := strings.LastIndex(q, "q") + 1
			if strings.Contains(q, "$1") {
				token = "$1"
				pos = strings.Index(q, "$1") + 1
			}
			if strings.Contains(q, "'end'") {
				token = "'end'"
				pos = strings.Index(q, "'end'") + 1
			}
			if strings.Contains(q, "nonsense") {
				token = "nonsense"
				pos = strings.Index(q, token) + 1
			}
			if strings.HasSuffix(q, "BEFORE") {
				token = ""
				pos = len([]rune(q)) + 1
			}
			if strings.Contains(q, "IF NOT") {
				token = "IF"
				pos = strings.Index(q, "IF") + 1
			}
			want := enumAlterDiagnostic("42601", `syntax error at or near "`+token+`"`)
			if token == "" {
				want.Message = "syntax error at end of input"
			}
			want.Position = strconv.Itoa(pos)
			for _, prepared := range []bool{false, true} {
				phase := "execute"
				if prepared {
					phase = "prepare"
				}
				enumAlterError(t, f, f.A, q, prepared, false, phase, want)
			}
		}
		for _, prepared := range []bool{false, true} {
			*f.Notices = nil
			s := fixture.Exec("ALTER TYPE mood ADD VALUE IF NOT EXISTS 'q' BEFORE 'absent'", 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `enum label "q" already exists, skipping`}
			if len(*f.Notices) != 1 || (*f.Notices)[0] != want {
				t.Errorf("notices got %#v want %#v", *f.Notices, want)
			}
		}
		for _, label := range []string{strings.Repeat("x", 63), strings.Repeat("é", 31) + "x", "", "Case", " case ", "it's", `back\slash`} {
			q := "ALTER TYPE mood ADD VALUE '" + strings.ReplaceAll(label, "'", "''") + "'"
			enumAlterExec(t, f, f.A, q)
		}
		// PostgreSQL's reset public schema is restricted; explicit setup USAGE
		// admits lookup so this witness reaches owner denial, not schema hiding.
		if pg {
			enumAlterExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO alter_stranger")
		}
		enumAlterExec(t, f, f.A, "SET SESSION AUTHORIZATION alter_stranger")
		for _, prepared := range []bool{false, true} {
			enumAlterError(t, f, f.A, "ALTER TYPE mood ADD VALUE 'denied'", prepared, false, "execute", enumAlterDiagnostic("42501", "must be owner of type mood"))
		}
		enumAlterExec(t, f, f.A, "RESET SESSION AUTHORIZATION")
		enumAlterRows(t, f, f.A, "SELECT 'q'::mood AS v", "q")
		enumAlterAuthorization(t, f, pg)
	})
}
