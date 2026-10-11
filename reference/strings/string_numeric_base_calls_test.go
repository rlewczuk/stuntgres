package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringNumericBaseCallDecorations(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			name string
			want string
		}{
			{"SELECT to_bin(VARIADIC 1)", "to_bin", "1"},
			{"SELECT to_oct(VARIADIC 1)", "to_oct", "1"},
			{"SELECT to_hex(VARIADIC 1)", "to_hex", "1"},
			{"SELECT to_hex(VARIADIC 1::bigint)", "to_hex", "1"},
			{"SELECT md5(VARIADIC 'abc')", "md5", "900150983cd24fb0d6963f7d28e17f72"},
			{"SELECT md5(VARIADIC 'abc'::text)", "md5", "900150983cd24fb0d6963f7d28e17f72"},
			{"SELECT md5(VARIADIC 'abc'::bytea)", "md5", "900150983cd24fb0d6963f7d28e17f72"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(tc.q, []string{tc.name}, [][]any{{tc.want}}))
			})
		}
		stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(
			`SELECT "to_hex"(1),"md5"('abc')`,
			[]string{"to_hex", "md5"}, [][]any{{"1", "900150983cd24fb0d6963f7d28e17f72"}}))
		for _, tc := range []struct {
			q         string
			signature string
		}{
			{"SELECT to_hex(value => 1)", "to_hex(value => integer)"},
			{"SELECT to_hex(value := 1)", "to_hex(value => integer)"},
			{"SELECT md5(value => 'abc')", "md5(value => unknown)"},
			{"SELECT md5(value := 'abc')", "md5(value => unknown)"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := stringNumericBaseDiagnostic("42883", "function "+tc.signature+" does not exist", fixture.BinaryHashHint, "8")
				stringNumericBaseBoth(t, p, b, c, fixture.ErrorDiagnostic(tc.q, d))
			})
		}
		d := stringNumericBaseDiagnostic("42725", "function to_hex(unknown) is not unique", stringNumericBaseAmbiguousHint, "8")
		stringNumericBaseBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT to_hex(VARIADIC NULL)", d))
	})
}

func TestReferenceStringNumericBaseInferenceOrder(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q   string
			typ string
		}{
			{"SELECT to_hex($1),$1::integer", "unknown"},
			{"SELECT to_hex($1)::text", "unknown"},
			{"SELECT to_hex($1::smallint)", "smallint"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := stringNumericBaseDiagnostic("42725", "function to_hex("+tc.typ+") is not unique", stringNumericBaseAmbiguousHint, "8")
				stringNumericBaseBoth(t, p, b, c, fixture.ErrorDiagnostic(tc.q, d, int64(-1)))
			})
		}
	})
}
