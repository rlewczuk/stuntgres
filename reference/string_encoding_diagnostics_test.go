package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceStringEncodingDiagnostics(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		cases := []struct{ expr, code, message string }{
			{"chr(-1)", "22023", "character number must be positive"}, {"chr(0)", "54000", "null character not permitted"},
			{"chr(55296)", "54000", "requested character not valid for encoding: 55296"}, {"chr(56319)", "54000", "requested character not valid for encoding: 56319"},
			{"chr(56320)", "54000", "requested character not valid for encoding: 56320"}, {"chr(57343)", "54000", "requested character not valid for encoding: 57343"},
			{"chr(1114112)", "54000", "requested character too large for encoding: 1114112"}, {"chr(2147483647)", "54000", "requested character too large for encoding: 2147483647"},
			{"to_ascii('')", "0A000", "encoding conversion from UTF8 to ASCII not supported"}, {"to_ascii('abc')", "0A000", "encoding conversion from UTF8 to ASCII not supported"},
		}
		for _, tc := range []struct{ arg, canonical string }{{"'UTF8'", "UTF8"}, {"6", "UTF8"}, {"'SQL_ASCII'", "SQL_ASCII"}, {"0", "SQL_ASCII"}, {"'muleinternal'", "MULE_INTERNAL"}, {"7", "MULE_INTERNAL"}, {"'SJIS'", "SJIS"}, {"35", "SJIS"}} {
			cases = append(cases, struct{ expr, code, message string }{"to_ascii(''," + tc.arg + ")", "0A000", "encoding conversion from " + tc.canonical + " to ASCII not supported"})
		}
		for _, arg := range []string{"CP1250", "nope", "8"} {
			cases = append(cases, struct{ expr, code, message string }{"to_ascii('abc','" + arg + "')", "42704", arg + " is not a valid encoding name"})
		}
		for _, arg := range []string{"42", "-1"} {
			cases = append(cases, struct{ expr, code, message string }{"to_ascii('abc'," + arg + ")", "42704", arg + " is not a valid encoding code"})
		}
		for _, tc := range cases {
			t.Run(tc.expr, func(t *testing.T) {
				stringEncodingFailure(t, p, b, c, "SELECT "+tc.expr, controlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
		for _, tc := range []struct{ source, code, message string }{
			{`\0000`, "22023", "invalid Unicode code point: 0000"}, {`\u0000`, "22023", "invalid Unicode code point: 0000"}, {`\+000000`, "22023", "invalid Unicode code point: 0000"}, {`\U00000000`, "22023", "invalid Unicode code point: 0000"},
			{`\+110000`, "22023", "invalid Unicode code point: 110000"}, {`\UFFFFFFFF`, "22023", "invalid Unicode code point: FFFFFFFF"}, {`\D83D\+110000`, "22023", "invalid Unicode code point: 110000"},
			{`\DE42`, "42601", "invalid Unicode surrogate pair"}, {`\D83D`, "42601", "invalid Unicode surrogate pair"}, {`\D83Dx`, "42601", "invalid Unicode surrogate pair"}, {`\D83Dé`, "42601", "invalid Unicode surrogate pair"},
			{`\D83D\\`, "42601", "invalid Unicode surrogate pair"}, {`\D83D\0041`, "42601", "invalid Unicode surrogate pair"}, {`\D83D\D83D`, "42601", "invalid Unicode surrogate pair"},
			{`\`, "42601", "invalid Unicode escape"}, {`\n`, "42601", "invalid Unicode escape"}, {`\123`, "42601", "invalid Unicode escape"}, {`\uZZZZ`, "42601", "invalid Unicode escape"}, {`\+12345`, "42601", "invalid Unicode escape"}, {`\U000000G1`, "42601", "invalid Unicode escape"}, {`\D83D\xyz`, "42601", "invalid Unicode escape"},
		} {
			t.Run(tc.source, func(t *testing.T) {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				if tc.message == "invalid Unicode escape" {
					d.Hint = `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`
				}
				stringEncodingFailure(t, p, b, c, "SELECT unistr($$"+tc.source+"$$)", d, false)
				q := "SELECT unistr($1) AS v"
				bad := referenceErrorDiagnostic(q, d, tc.source)
				bad.selectRows, bad.errorStage = true, "execute"
				valid := referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"🙂"}}, `\D83D\DE42`)
				stringEncodingHeld(t, p, b, c, q, []referenceStep{valid, bad, valid})
			})
		}
		for _, fn := range []string{"ascii", "unistr", "unicode_assigned", "to_ascii"} {
			q := "SELECT " + fn + "($1) AS v"
			if fn == "to_ascii" {
				q = "SELECT to_ascii($1,'LATIN1') AS v"
			}
			typ, want := "TEXT", any("A")
			if fn == "ascii" {
				typ, want = "INT4", int64(65)
			}
			if fn == "unicode_assigned" {
				typ, want = "BOOL", true
			}
			valid := referenceSelect(q, []string{"v"}, []string{typ}, [][]any{{want}}, "A")
			for _, tc := range []struct{ source, bytes string }{{"A\x00", "0x00"}, {"A\xff", "0xff"}} {
				d := controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+tc.bytes)
				d.Context = "unnamed portal parameter $1"
				bad := referenceErrorDiagnostic(q, d, tc.source)
				bad.selectRows, bad.errorStage = true, "execute"
				stringEncodingFailure(t, p, b, c, q, d, false, tc.source)
				stringEncodingHeld(t, p, b, c, q, []referenceStep{valid, bad, valid})
			}
		}
		q := "SELECT chr($1) AS v"
		valid := referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}}, 65)
		for _, tc := range []struct{ arg, code, message, context string }{
			{"0", "54000", "null character not permitted", ""}, {"-1", "22023", "character number must be positive", ""},
			{"bad", "22P02", `invalid input syntax for type integer: "bad"`, "unnamed portal parameter $1 = '...'"},
			{"2147483648", "22003", `value "2147483648" is out of range for type integer`, "unnamed portal parameter $1 = '...'"},
		} {
			d := controlsReferenceDiagnostic(tc.code, tc.message)
			d.Context = tc.context
			bad := referenceErrorDiagnostic(q, d, tc.arg)
			bad.selectRows, bad.errorStage = true, "execute"
			stringEncodingFailure(t, p, b, c, q, d, false, tc.arg)
			stringEncodingHeld(t, p, b, c, q, []referenceStep{valid, bad, valid})
		}
	})
}

func TestReferenceStringEncodingTyping(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		cases := []struct{ call, signature string }{
			{"ascii()", "ascii()"}, {"ascii('a','b')", "ascii(unknown, unknown)"}, {"chr()", "chr()"}, {"chr(1,2)", "chr(integer, integer)"},
			{"to_ascii()", "to_ascii()"}, {"to_ascii('a',8,9)", "to_ascii(unknown, integer, integer)"}, {"unistr()", "unistr()"}, {"unistr('a','b')", "unistr(unknown, unknown)"},
			{"unicode_assigned()", "unicode_assigned()"}, {"unicode_assigned('a','b')", "unicode_assigned(unknown, unknown)"}, {`"ASCII"('a')`, "ASCII(unknown)"},
		}
		for _, fn := range []string{"ascii", "unistr", "unicode_assigned", "to_ascii"} {
			for _, arg := range []struct{ sql, typ string }{{"1", "integer"}, {"true", "boolean"}, {"'a'::bytea", "bytea"}} {
				cases = append(cases, struct{ call, signature string }{fn + "(" + arg.sql + ")", fn + "(" + arg.typ + ")"})
			}
		}
		for _, arg := range []struct{ sql, typ string }{{"65::bigint", "bigint"}, {"65::numeric", "numeric"}, {"'65'::text", "text"}, {"true", "boolean"}, {"'a'::bytea", "bytea"}} {
			cases = append(cases, struct{ call, signature string }{"chr(" + arg.sql + ")", "chr(" + arg.typ + ")"})
		}
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fmt.Sprint(8 + len([]rune(prefix)))
					stringEncodingFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, tc := range []struct {
			q, code, message string
			position         int
		}{
			{"SELECT chr('bad')", "22P02", `invalid input syntax for type integer: "bad"`, 12},
			{"SELECT chr('2147483648')", "22003", `value "2147483648" is out of range for type integer`, 12},
			{"SELECT ascii(missing)", "42703", `column "missing" does not exist`, 14},
			{"SELECT no_se_schema.ascii('a')", "3F000", `schema "no_se_schema" does not exist`, 8},
			{"SELECT chr(NULL,missing)", "42703", `column "missing" does not exist`, 17},
			{"SELECT to_ascii(NULL,chr('bad'))", "22P02", `invalid input syntax for type integer: "bad"`, 26},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fmt.Sprint(tc.position + len([]rune(prefix)))
				stringEncodingFailure(t, p, b, c, prefix+tc.q, d, true)
			}
		}
		// Complete UTF8 NAME clipping, not a byte-truncated or unlimited diagnostic.
		source := strings.Repeat("é", 32)
		d := controlsReferenceDiagnostic("42704", strings.Repeat("é", 31)+" is not a valid encoding name")
		stringEncodingFailure(t, p, b, c, "SELECT to_ascii('abc',$1)", d, false, source)
	})
}
