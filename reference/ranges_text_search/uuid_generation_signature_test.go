package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceUUIDGenerationSignatures(t *testing.T) {
	uuidGenerationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ query, signature string }{
			{"SELECT uuidv4(1)", "uuidv4(integer)"},
			{"SELECT gen_random_uuid(1)", "gen_random_uuid(integer)"},
			{"SELECT uuidv7(1)", "uuidv7(integer)"},
			{"SELECT uuidv7('0'::text)", "uuidv7(text)"},
			{"SELECT uuidv7(interval '0',interval '0')", "uuidv7(interval, interval)"},
			{"SELECT uuidv7(bad=>interval '0')", "uuidv7(bad => interval)"},
			{"SELECT public.uuidv7()", "public.uuidv7()"},
		} {
			t.Run(tc.signature, func(t *testing.T) {
				_, err := c.PrepareContext(p.Ctx, tc.query)
				want := uuidGenerationDiagnostic("42883", "function "+tc.signature+" does not exist", "")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				uuidGenerationError(t, err, want)
			})
		}
		duplicate := "SELECT uuidv7(shift=>interval '0',shift=>interval '1 day')"
		_, err := c.PrepareContext(p.Ctx, duplicate)
		want := uuidGenerationDiagnostic("42601", `argument name "shift" used more than once`, "")
		want.Position = strconv.Itoa(utf8.RuneCountInString(duplicate[:strings.LastIndex(duplicate, "shift=>")]) + 1)
		uuidGenerationError(t, err, want)
		tooMany := "SELECT uuidv7(" + strings.Repeat("1,", 100) + "1)"
		_, err = c.PrepareContext(p.Ctx, tooMany)
		want = uuidGenerationDiagnostic("54023", "cannot pass more than 100 arguments to a function", "")
		want.Position = "8"
		uuidGenerationError(t, err, want)
	})
}
