package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// All new locations/stages below are independently specified inference until
// the root observes the pinned oracle. Full Diagnostic also asserts empty fields.
func TestReferenceIsnConversionDiagnostics(t *testing.T) {
	for targetIndex := 1; targetIndex < len(isnConversionRoles); targetIndex++ {
		target := isnConversionRoles[targetIndex]
		t.Run("typed_to_"+target.name, func(t *testing.T) {
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				for _, tc := range []struct {
					input, number, family string
					admitted              bool
				}{
					{"9780123456786", "9780123456786", "EAN13(ISBN)", targetIndex == 1 || targetIndex == 2},
					{"9791234567896", "9791234567896", "EAN13(ISBN)", targetIndex == 1 || targetIndex == 2},
					{"9790123456785", "9790123456785", "EAN13(ISMN)", targetIndex == 3 || targetIndex == 4},
					{"9771234567003", "9771234567003", "EAN13(ISSN)", targetIndex == 5 || targetIndex == 6},
					{"0220356483481", "0220356483481", "EAN13(UPC)", targetIndex == 7},
					{"1234567890128", "1234567890128", "EAN13", false},
					{"123-456789012-0!", "1234567890128", "EAN13", false},
					{"9790123456780!", "9790123456785", "EAN13(ISMN)", targetIndex == 3 || targetIndex == 4},
				} {
					if tc.admitted {
						continue
					}
					want := isnConversionDiagnostic("22P02", "cannot cast "+tc.family+" to "+isnConversionInputFamily(target.name)+" for number: \""+tc.number+"\"")
					for _, expression := range []string{"$1::ean13::" + target.name, target.name + "($1::ean13)"} {
						q := "SELECT " + expression + " AS v"
						held := fixture.IdentityHeld(t, p.Ctx, c, q)
						defer held.Statement.Close()
						for _, value := range []any{tc.input, []byte(tc.input)} {
							s := isnConversionRuntimeError(q, want, value)
							s.SelectRows = true
							run(s)
							run(fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
						good := fixture.Ean13Select(b, q, []string{"v"}, []string{strings.ToUpper(target.name)}, [][]any{{target.output}}, nil, target.compact)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
					}
					for _, expression := range []string{isnConversionLiteral(tc.input) + "::ean13::" + target.name, target.name + "(" + isnConversionLiteral(tc.input) + "::ean13)"} {
						s := isnConversionRuntimeError("SELECT "+expression, want)
						s.SelectRows = true
						run(s)
						run(fixture.Prepared(s))
					}
				}
			})
		})
	}
	t.Run("input_origin", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			message := `invalid check digit for ISBN number: "9781234567890", should be 7`
			for _, q := range []string{"SELECT '9781234567890'::isbn13", "/*é*/ SELECT '9781234567890'::isbn13"} {
				want := isnConversionDiagnostic("22P02", message)
				want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'978")]) + 1)
				for _, prepared := range []bool{false, true} {
					s := isnConversionRuntimeError(q, want)
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			q := "SELECT $1::isbn13 AS v"
			held := fixture.IdentityHeld(t, p.Ctx, c, q)
			defer held.Statement.Close()
			want := isnConversionDiagnostic("22P02", message)
			want.Context = "unnamed portal parameter $1 = '...'"
			for _, arg := range []any{"9781234567890", []byte("9781234567890")} {
				s := isnConversionRuntimeError(q, want, arg)
				s.SelectRows = true
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Ean13Select(b, q, []string{"v"}, []string{"ISBN13"}, [][]any{{"978-1-234-56789-7"}}, nil, "123456789X"))
		})
	})
	t.Run("closed_admission", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			for _, tc := range []struct{ q, code, message, token, hint string }{
				{"SELECT '123456789X'::isbn = 'M123456785'::ismn", "42883", "operator does not exist: isbn = ismn", "=", "No operator matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT '123456789X'::isbn::ismn", "42846", "cannot cast type isbn to ismn", "::ismn", ""},
				{"SELECT NULL::isbn::ismn", "42846", "cannot cast type isbn to ismn", "::ismn", ""},
				{"SELECT NULL::isbn = NULL::upc", "42883", "operator does not exist: isbn = upc", "=", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			} {
				want := isnConversionDiagnostic(tc.code, tc.message)
				want.Hint, want.Position = tc.hint, fixture.Position(strings.Index(tc.q, tc.token)+1)
				for _, prepared := range []bool{false, true} {
					s := isnConversionRuntimeError(tc.q, want)
					s.SelectRows = true
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
