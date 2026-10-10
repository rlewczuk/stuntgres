package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

const stringNumericBaseAmbiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."

func TestReferenceStringNumericBaseDiagnostics(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, fn := range []string{"to_bin", "to_oct", "to_hex"} {
			for _, arg := range []struct{ expr, typ string }{
				{"NULL", "unknown"}, {"'1'", "unknown"}, {"$1", "unknown"}, {"1::smallint", "smallint"},
			} {
				for _, prefix := range []struct{ sql, pos string }{{"", "8"}, {"/*é🙂*/ ", "15"}} {
					q := prefix.sql + "SELECT " + fn + "(" + arg.expr + ")"
					t.Run(q, func(t *testing.T) {
						d := stringNumericBaseDiagnostic("42725", "function "+fn+"("+arg.typ+") is not unique", stringNumericBaseAmbiguousHint, prefix.pos)
						var args []any
						if arg.expr == "$1" {
							args = []any{int64(42)}
						}
						stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic(q, d, args...))
					})
				}
			}
			for _, arg := range []struct{ expr, typ string }{
				{"", ""}, {"1,2", "integer, integer"}, {"'1'::text", "text"},
				{"true", "boolean"}, {"'1'::bytea", "bytea"}, {"1::real", "real"},
				{"1::double precision", "double precision"}, {"1.0", "numeric"},
				{"9223372036854775808", "numeric"},
			} {
				q := "SELECT " + fn + "(" + arg.expr + ")"
				t.Run(q, func(t *testing.T) {
					d := stringNumericBaseDiagnostic("42883", "function "+fn+"("+arg.typ+") does not exist", binaryHashHint, "8")
					stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic(q, d))
				})
			}
		}
		for _, tc := range []struct{ q, code, message, hint, pos string }{
			{"SELECT md5(1)", "42883", "function md5(integer) does not exist", binaryHashHint, "8"},
			{"SELECT md5()", "42883", "function md5() does not exist", binaryHashHint, "8"},
			{"SELECT md5('a','b')", "42883", "function md5(unknown, unknown) does not exist", binaryHashHint, "8"},
			{"SELECT to_hex('bad'::integer)", "22P02", `invalid input syntax for type integer: "bad"`, "", "15"},
			{"SELECT to_hex('2147483648'::integer)", "22003", `value "2147483648" is out of range for type integer`, "", "15"},
			{"SELECT to_hex(NULL,missing)", "42703", `column "missing" does not exist`, "", "20"},
			{"SELECT md5(NULL,missing)", "42703", `column "missing" does not exist`, "", "17"},
			{`SELECT md5('\x0'::bytea)`, "22023", "invalid hexadecimal data: odd number of digits", "", "12"},
			{`SELECT "TO_HEX"(1)`, "42883", "function TO_HEX(integer) does not exist", binaryHashHint, "8"},
			{`SELECT "To_hex"(1)`, "42883", "function To_hex(integer) does not exist", binaryHashHint, "8"},
			{`SELECT "MD5"('abc')`, "42883", "function MD5(unknown) does not exist", binaryHashHint, "8"},
			{"SELECT snb_absent.to_hex(1)", "3F000", `schema "snb_absent" does not exist`, "", "8"},
			{"SELECT snb_absent.md5('abc')", "3F000", `schema "snb_absent" does not exist`, "", "8"},
			{"SELECT public.to_hex(1)", "42883", "function public.to_hex(integer) does not exist", binaryHashHint, "8"},
			{"SELECT public.md5('abc')", "42883", "function public.md5(unknown) does not exist", binaryHashHint, "8"},
			{"SELECT md5($1),sha256($1)", "42883", "function sha256(text) does not exist", binaryHashHint, "16"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				var args []any
				if strings.Contains(tc.q, "$1") {
					args = []any{"abc"}
				}
				d := stringNumericBaseDiagnostic(tc.code, tc.message, tc.hint, tc.pos)
				stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic(tc.q, d, args...))
			})
		}
		for _, fn := range []string{"to_bin", "to_oct", "to_hex", "md5"} {
			q := "SELECT " + fn + "(" + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + ")"
			t.Run(fn+"/limit", func(t *testing.T) {
				d := stringNumericBaseDiagnostic("54023", "cannot pass more than 100 arguments to a function", "", "8")
				stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic(q, d))
			})
			q = "SELECT " + fn + "(missing," + strings.TrimSuffix(strings.Repeat("1,", 100), ",") + ")"
			pos := "15"
			if fn == "md5" {
				pos = "12"
			}
			t.Run(fn+"/child-before-limit", func(t *testing.T) {
				d := stringNumericBaseDiagnostic("42703", `column "missing" does not exist`, "", pos)
				stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic(q, d))
			})
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE snb_errors(i smallint,s text)", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO snb_errors VALUES(NULL,NULL)", 1))
			}
			for _, fn := range []string{"to_bin", "to_oct", "to_hex"} {
				t.Run(fn+"/source", func(t *testing.T) {
					stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic("SELECT "+fn+"(i) FROM snb_errors", stringNumericBaseDiagnostic("42725", "function "+fn+"(smallint) is not unique", stringNumericBaseAmbiguousHint, "8")))
					stringNumericBaseBoth(t, p, b, c, referenceErrorDiagnostic("SELECT "+fn+"(s) FROM snb_errors", stringNumericBaseDiagnostic("42883", "function "+fn+"(text) does not exist", binaryHashHint, "8")))
				})
			}
		}
		stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT pg_catalog.to_bin(42),pg_catalog.to_oct(42),pg_catalog.to_hex(42),pg_catalog.md5('abc')", []string{"to_bin", "to_oct", "to_hex", "md5"}, [][]any{{"101010", "52", "2a", "900150983cd24fb0d6963f7d28e17f72"}}))
	})
}
