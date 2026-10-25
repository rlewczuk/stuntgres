package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func upcScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	ean13Scenario(t, body)
}

// PostgreSQL 18.6 ean2UPC truncation: see docs/POSTGRES_REGRESSION.md.
// Independently specified native expectations always retain the marker.
func upcOutput(b fixture.Backend, native string) string {
	if b.PG && len(native) == 13 && native[12] == '!' && native[0] >= '0' && native[0] <= '9' { return native[:12] }
	return native
}

func upcCell(b fixture.Backend, cell any) any {
	if text, ok := cell.(string); ok { return upcOutput(b, text) }
	return cell
}

func upcSelect(b fixture.Backend, q string, names, types []string, expected [][]any, nullable []bool, args ...any) fixture.Step {
	rows := make([][]any, len(expected))
	for i, row := range expected {
		rows[i] = make([]any, len(row))
		for j, cell := range row { rows[i][j] = upcCell(b, cell) }
	}
	return fixture.Ean13Select(b, q, names, types, rows, nullable, args...)
}

func upcBadInputs() []struct{ input, code, message string } {
	out := []struct{ input, code, message string }{
		{"123456789010", "22P02", `invalid check digit for UPC number: "123456789010", should be 2`},
		{"0123456789010", "22P02", `invalid check digit for UPC number: "0123456789010", should be 2`},
		{"12345678901234", "22003", `value "12345678901234" is out of range for UPC type`},
	}
	for _, tc := range []struct{ input, family string }{
		{"9780123456786", "ISBN"}, {"9790123456785", "ISMN"}, {"9771234567898", "ISSN"}, {"1234567890128", "EAN13"},
		{"123456789X", "ISBN"}, {"M123456785", "ISMN"}, {"12345679", "ISSN"},
		{"9780123456780", "ISBN"}, {"9780123456780!", "ISBN"}, {"9790123456780!", "ISMN"}, {"9771234567890!", "ISSN"}, {"1234567890120!", "EAN13"},
	} {
		out = append(out, struct{ input, code, message string }{tc.input, "22P02", "cannot cast "+tc.family+" to UPC for number: \""+tc.input+"\""})
	}
	for _, input := range []string{"", "bad", "12345678901", "123456789012 ", "123456789012-", "123\t456789012", "123\n456789012", "123456789A12", "!123456789012", "123456789012!!", "123456?89012", "12345678901X", "123456789012junk"} {
		out = append(out, struct{ input, code, message string }{input, "22P02", "invalid input syntax for UPC number: \""+input+"\""})
	}
	return out
}

