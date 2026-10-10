package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceBinaryConversionDiagnostics(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		cases := []struct{ expr, code, message, hint string }{
			{`encode('a'::bytea,' hex')`, "22023", `unrecognized encoding: " hex"`, ""},
			{`decode('a','hex')`, "22023", "invalid hexadecimal data: odd number of digits", ""},
			{`decode('0 0','hex')`, "22023", `invalid hexadecimal digit: " "`, ""},
			{`decode('0é','hex')`, "22023", `invalid hexadecimal digit: "é"`, ""},
			{`decode('界0','hex')`, "22023", `invalid hexadecimal digit: "界"`, ""},
			{`decode('0😀','hex')`, "22023", `invalid hexadecimal digit: "😀"`, ""},
			{`decode('Zg','base64')`, "22023", "invalid base64 end sequence", "Input data is missing padding, is truncated, or is otherwise corrupted."},
			{`decode('=AAA','base64')`, "22023", `unexpected "=" while decoding base64 sequence`, ""},
			{`decode('é','base64')`, "22023", `invalid symbol "é" found while decoding base64 sequence`, ""},
			{`decode('AAAA` + "\v" + `','base64')`, "22023", `invalid symbol "` + "\v" + `" found while decoding base64 sequence`, ""},
			{`decode('\400','escape')`, "22P02", "invalid input syntax for type bytea", ""},
			{`convert('a'::bytea,'bad','worse')`, "22023", `invalid source encoding name "bad"`, ""},
			{`convert('\x00'::bytea,'UTF8','bad')`, "22023", `invalid destination encoding name "bad"`, ""},
			{`convert('a'::bytea,'LATIN1','LATIN2')`, "42883", `default conversion function for encoding "LATIN1" to "LATIN2" does not exist`, ""},
			{`convert('a'::bytea,'UTF8','MULE_INTERNAL')`, "42883", `default conversion function for encoding "UTF8" to "MULE_INTERNAL" does not exist`, ""},
			{`convert('a'::bytea,'MULE_INTERNAL','UTF8')`, "42883", `default conversion function for encoding "MULE_INTERNAL" to "UTF8" does not exist`, ""},
			{`convert('\x00'::bytea,'UTF8','MULE_INTERNAL')`, "22021", `invalid byte sequence for encoding "UTF8": 0x00`, ""},
			{`convert('\xff'::bytea,'SQL_ASCII','UTF8')`, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, ""},
			{`convert_to('€','LATIN1')`, "22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`, ""},
			{`convert('\x81'::bytea,'WIN1252','UTF8')`, "22P05", `character with byte sequence 0x81 in encoding "WIN1252" has no equivalent in encoding "UTF8"`, ""},
		}
		for _, f := range []string{"hex ", "base-64", "base32hex", "base64url", strings.Repeat("x", 80)} {
			cases = append(cases, struct{ expr, code, message, hint string }{"encode('a'::bytea,'" + f + "')", "22023", `unrecognized encoding: "` + f + `"`, ""})
		}
		for _, v := range []string{`\`, `\0`, `\00`, `\378`, `\q`} {
			cases = append(cases, struct{ expr, code, message, hint string }{"decode('" + v + "','escape')", "22P02", "invalid input syntax for type bytea", ""})
		}
		for _, tc := range cases {
			t.Run(tc.expr, func(t *testing.T) {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				stringEncodingFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.expr, d, false)
			})
		}
		for _, name := range []string{"EUC_JP", "EUC_CN", "EUC_KR", "EUC_TW", "EUC_JIS_2004", "SJIS", "BIG5", "GBK", "UHC", "GB18030", "JOHAB", "SHIFT_JIS_2004"} {
			t.Run(name+"_truncated", func(t *testing.T) {
				lead := byte(0xa1)
				if name == "SJIS" || name == "SHIFT_JIS_2004" || name == "GB18030" || name == "JOHAB" {
					lead = 0x81
				}
				d := controlsReferenceDiagnostic("22021", fmt.Sprintf(`invalid byte sequence for encoding "%s": 0x%02x`, name, lead))
				stringEncodingFailure(t, p, b, c, "SELECT convert($1,$2,$2)", d, false, []byte{lead}, name)
			})
		}
		for _, tc := range []struct{ expr, want string }{{`convert(''::bytea,'LATIN1','LATIN2')`, ""}, {`convert(''::bytea,'UTF8','MULE_INTERNAL')`, ""}, {`convert('\xff'::bytea,'SQL_ASCII','LATIN1')`, "\xff"}, {`convert('\xc3a9'::bytea,'UTF8','SQL_ASCII')`, "é"}, {`convert('\x81'::bytea,'WIN1252','WIN1252')`, "\x81"}} {
			binaryConversionValue(t, p, b, c, tc.expr, "BYTEA", tc.want)
		}
	})
}

func TestReferenceBinaryConversionPadding(t *testing.T) {
	// REL_18_0 encode.c quartet/end semantics allow these unusual sequences.
	// Independent fixed byte expectations were validated against PostgreSQL18.0.
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ input, want string }{{"AA=A", "\x00"}, {"AA==AAAA", "\x00\x00"}, {"AB==", "\x00"}, {"AA= =", "\x00"}} {
			t.Run(tc.input, func(t *testing.T) {
				binaryConversionBoth(t, p, b, c, referenceSelect("SELECT decode($1,'base64') AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}}, tc.input))
			})
		}
		for _, input := range []string{"AAAA====", "AA===", "AA==A", "A===", "AAA", "AAAA=", "AA==\f"} {
			t.Run(input, func(t *testing.T) {
				d := controlsReferenceDiagnostic("22023", `unexpected "=" while decoding base64 sequence`)
				if input == "AAA" || input == "AA==A" || input == "AA===" {
					d.Message = "invalid base64 end sequence"
					d.Hint = "Input data is missing padding, is truncated, or is otherwise corrupted."
				}
				if input == "AA==\f" {
					d.Message = "invalid symbol \"\f\" found while decoding base64 sequence"
				}
				stringEncodingFailure(t, p, b, c, "SELECT decode($1,'base64')", d, false, input)
			})
		}
	})
}
