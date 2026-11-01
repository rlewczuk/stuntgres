package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Independently specified locations, execution stages and empty diagnostic
// fields are validated against the pinned PostgreSQL 18.6 oracle.
func TestReferenceIsnValidationDiagnostics(t *testing.T) {
	for i, role := range isnConversionRoles {
		t.Run(role.name, func(t *testing.T) {
			isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				isnValidationMode(run, false)
				for _, tc := range []struct {
					input string
					weak bool
					d fixture.Diagnostic
				}{
					{isnValidationBad[i], false, isnValidationChecksum(i, isnValidationBad[i])},
					{"bad", true, isnConversionDiagnostic("22P02", "invalid input syntax for "+isnConversionInputFamily(role.name)+` number: "bad"`)},
					{"", true, isnConversionDiagnostic("22P02", "invalid input syntax for "+isnConversionInputFamily(role.name)+` number: ""`)},
				} {
					isnValidationMode(run, tc.weak)
					for _, prefix := range []string{"", "/*é日*/ "} {
						q := prefix + "SELECT " + isnConversionLiteral(tc.input) + "::" + role.name
						d := tc.d
						d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
						for _, prepared := range []bool{false, true} {
							s := isnValidationQueryError(q, d)
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							}
							run(s)
						}
					}
					for _, expression := range []string{"$1::" + role.name, "$1::text::" + role.name, "$2::" + role.name} {
						q := "SELECT " + expression + " AS v"
						if strings.HasPrefix(expression, "$2") {
							q = "SELECT $1::integer AS n," + expression + " AS v"
						}
						held := fixture.IdentityHeld(t, p.Ctx, c, q)
						for _, arg := range []any{tc.input, []byte(tc.input)} {
							d := tc.d
							args := []any{arg}
							if !strings.Contains(expression, "::text") {
								number, value := "1", "..."
								if tc.input == "" {
									value = ""
								}
								if strings.HasPrefix(expression, "$2") {
									number, args = "2", []any{int64(7), arg}
								}
								d.Context = "unnamed portal parameter $" + number + " = '" + value + "'"
							}
							s := isnValidationQueryError(q, d, args...)
							run(s)
							run(fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
						held.Statement.Close()
					}
				}
			})
		})
	}
	t.Run("family_range_boolean", func(t *testing.T) {
		isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			isnValidationMode(run, true)
			for _, tc := range []struct{ q, input, code, message string }{
				{"SELECT $1::text::ean13", "12345678901234", "22003", `value "12345678901234" is out of range for EAN13 type`},
				{"SELECT $1::text::isbn", "9790123456780", "22P02", `cannot cast ISMN to ISBN for number: "9790123456780"`},
			} {
				isnConversionRoutes(t, p, b, c, isnValidationQueryError(tc.q, isnConversionDiagnostic(tc.code, tc.message), tc.input))
			}
			run(isnConversionRuntimeError("SET isn.weak='bogus'", isnConversionDiagnostic("22023", `parameter "isn.weak" requires a Boolean value`)))
			isnValidationSetting(run, true)
		})
	})
	t.Run("resolution", func(t *testing.T) {
		isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			for _, tc := range []struct{ call, signature, state string }{
				{"is_valid('2222222222222')", "is_valid(unknown)", "42725"},
				{"make_valid(NULL)", "make_valid(unknown)", "42725"},
				{"is_valid(1)", "is_valid(integer)", "42883"},
				{"make_valid(1)", "make_valid(integer)", "42883"},
				{"is_valid('x'::text)", "is_valid(text)", "42883"},
				{"make_valid(ARRAY[NULL::ean13])", "make_valid(ean13[])", "42883"},
				{"isn_weak(true,false)", "isn_weak(boolean, boolean)", "42883"},
				{"is_valid()", "is_valid()", "42883"},
			} {
				d := isnConversionDiagnostic(tc.state, "function "+tc.signature+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				if tc.state == "42725" {
					d.Message = "function " + tc.signature + " is not unique"
					d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
				}
				d.Position = fixture.Position(8)
				for _, prepared := range []bool{false, true} {
					s := isnValidationQueryError("SELECT "+tc.call, d)
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		})
	})
}
