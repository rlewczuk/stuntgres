package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVersionInformationDiagnostics(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE version_information_error_rows(id INT)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO version_information_error_rows VALUES(1),(2)", 2))
		for _, name := range []string{"version", "unicode_version", "icu_unicode_version"} {
			cases := []struct{ query, code, message, position, hint string }{
				{"SELECT " + name + "(1)", "42883", "function " + name + "(integer) does not exist", "8", hint},
				{"SELECT " + name + "(NULL)", "42883", "function " + name + "(unknown) does not exist", "8", hint},
				{"SELECT " + name + "('x'::text)", "42883", "function " + name + "(text) does not exist", "8", hint},
				{"SELECT " + name + "(1,2)", "42883", "function " + name + "(integer, integer) does not exist", "8", hint},
				{`SELECT "` + strings.ToUpper(name) + `"()`, "42883", "function " + strings.ToUpper(name) + "() does not exist", "8", hint},
				{"SELECT public." + name + "()", "42883", "function public." + name + "() does not exist", "8", hint},
				{"SELECT missing_version_information." + name + "()", "3F000", `schema "missing_version_information" does not exist`, "8", ""},
				{"/* é */ SELECT " + name + "(NULL)", "42883", "function " + name + "(unknown) does not exist", "16", hint},
				{"SELECT " + name + "(missing_version_argument)", "42703", `column "missing_version_argument" does not exist`, "9", ""},
				{"SELECT " + name + "(*)", "42809", name + "(*) specified, but " + name + " is not an aggregate function", "8", ""},
				{"SELECT " + name + "() OVER ()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "8", ""},
			}
			// Operand position follows the opening parenthesis, in characters.
			cases[8].position = fixture.Position(9 + len(name))
			if name == "unicode_version" {
				cases = append(cases, struct{ query, code, message, position, hint string }{
					"SELECT coalesce(unicode_version(1),'x')", "42883", "function unicode_version(integer) does not exist", "17", hint,
				})
			}
			for _, tc := range cases {
				t.Run(tc.query, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						label := "direct"
						if prepared {
							label = "prepare"
						}
						t.Run(label, func(t *testing.T) {
							s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
							s.Prepared, s.SelectRows = prepared, true
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						})
					}
				})
			}
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id FROM version_information_error_rows ORDER BY id",
			[]string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}
