package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func issnScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

// PostgreSQL 18.6 ean2ISSN truncation: see docs/POSTGRES_REGRESSION.md.
// Native expectations retain both the invalid flag and nonzero issue digits.
func issnOutput(b fixture.Backend, native string) string {
	if b.PG {
		switch native {
		case "1234-5679!", "977-1234-567-89-8", "977-1234-567-89-8!":
			return "1234-5679"
		}
	}
	return native
}

func issnSelect(b fixture.Backend, q string, names, types []string, expected [][]any, nullable []bool, args ...any) fixture.Step {
	rows := make([][]any, len(expected))
	for i, row := range expected {
		rows[i] = append([]any(nil), row...)
		for j, value := range row {
			if text, ok := value.(string); ok {
				rows[i][j] = issnOutput(b, text)
			}
		}
	}
	return fixture.Ean13Select(b, q, names, types, rows, nullable, args...)
}

func issnBadInputs() []struct{ input, code, message string } {
	out := []struct{ input, code, message string }{
		{"12345670", "22P02", `invalid check digit for ISSN number: "12345670", should be 9`},
		{"11448750", "22P02", `invalid check digit for ISSN number: "11448750", should be X`},
		{"9771234567000", "22P02", `invalid check digit for ISSN number: "9771234567000", should be 3`},
		{"9780123456786", "22P02", `cannot cast ISBN to ISSN for number: "9780123456786"`},
		{"9790123456785", "22P02", `cannot cast ISMN to ISSN for number: "9790123456785"`},
		{"0123456789012", "22P02", `cannot cast UPC to ISSN for number: "0123456789012"`},
		{"1234567890128", "22P02", `cannot cast EAN13 to ISSN for number: "1234567890128"`},
		{"12345678901234", "22003", `value "12345678901234" is out of range for ISSN type`},
	}
	for _, input := range []string{"", "1234567", "123456789", "postgresql...", "123A5679", "123\t45679", "123\n45679", "!12345679", "12345679!!", "123456?9", "1144875X ", "1144875? "} {
		out = append(out, struct{ input, code, message string }{input, "22P02", "invalid input syntax for ISSN number: \"" + input + "\""})
	}
	return out
}

func issnInstallUnpublished(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, routine string) {
	t.Helper()
	isbnInstallUnpublished(t, p, b, c, routine)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn13','_issn13')", []string{"typname"}, []string{"NAME"}, [][]any{}))
}
