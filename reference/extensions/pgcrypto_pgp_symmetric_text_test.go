package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPSymmetricBytea(t *testing.T) {
	symAnchor(t)
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"all_octets", all}, {"empty", []byte{}}, {"nul_ff", []byte{0, 255, 0, 128}}, {"ascii", []byte("ASCII is still binary")},
	} {
		for _, prepared := range []bool{false, true} {
			for _, three := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/three_%v", tc.name, symRouteName(prepared), three), func(t *testing.T) {
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
						options := "s2k-count=1024,unicode-mode=1,convert-crlf=1"
						var opt *string
						count := 0
						if three {
							opt, count = &options, 1024
						}
						packet := symEncrypt(t, p, c, prepared, true, tc.data, "binary-password", opt, symDefault(), 'b', tc.data, count)
						symDecrypt(t, p, c, r, packet, "binary-password", tc.data, false, opt)
						for _, route := range []bool{false, true} {
							pgpNoLeak(t, p, c, route, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Not text data", packet, "binary-password")
						}
					})
				})
			}
		}
	}
	for _, format := range []byte{'b', 'x', 0, 255, 't', 'u'} {
		t.Run(fmt.Sprintf("literal_%d", format), func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				payload := []byte{0, 255, '\r', '\n', 128}
				packet := symBuild("binary-password", pgpRFCLiteral(string(payload), "partial", format), symDefault(), "")
				options := "unicode-mode=1,convert-crlf=1"
				symDecrypt(t, p, c, r, packet, "binary-password", payload, false, nil)
				symDecrypt(t, p, c, r, packet, "binary-password", payload, false, &options)
			})
		})
	}
}

func TestReferencePgcryptoPGPSymmetricEncoding(t *testing.T) {
	symAnchor(t)
	t.Run("official_gnupg_nul", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			packet := symBase64(t, symNUL64)
			symDecrypt(t, p, c, r, packet, "key", []byte{'a', 0, 12}, false, nil)
			for _, prepared := range []bool{false, true} {
				d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`}
				symError(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", d, packet, "key")
			}
		})
	})
	// PostgreSQL reports the first invalid sequence, bounded by remaining bytes
	// and the encoding's sequence width, not all remaining input or a Go rune.
	for _, tc := range []struct{ name, payload, window string }{
		{"ff", "prefix\xfftail", "0xff"},
		{"nul", "prefix\x00tail", "0x00"},
		{"truncated_two", "\xc3", "0xc3"},
		{"bad_continuation", "\xc3(", "0xc3 0x28"},
		{"truncated_three", "\xe2\x82", "0xe2 0x82"},
		{"overlong", "\xe0\x80\x80", "0xe0 0x80 0x80"},
	} {
		for _, format := range []byte{'t', 'u'} {
			t.Run(fmt.Sprintf("%s/%c", tc.name, format), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					inner := pgpRFCLiteral(tc.payload, "", format)
					packet := symBuild("encoding-password", inner, symDefault(), "")
					symDecrypt(t, p, c, r, packet, "encoding-password", []byte(tc.payload), false, nil)
					d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.window}
					for _, prepared := range []bool{false, true} {
						symError(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", d, packet, "encoding-password")
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", symBuild("encoding-password", inner, symDefault(), "mdc"), "encoding-password")
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Not text data", symBuild("encoding-password", pgpRFCLiteral(tc.payload, "", 'b'), symDefault(), ""), "encoding-password")
					}
				})
			})
		}
	}
}

func TestReferencePgcryptoPGPSymmetricConversion(t *testing.T) {
	symAnchor(t)
	for _, tc := range []struct{ name, input string }{
		{"empty", ""}, {"lf", "\n"}, {"lone_cr", "a\rb"}, {"terminal_cr", "a\r"},
		{"crlf", "a\r\nb"}, {"crcrlf", "a\r\r\nb"}, {"official", "1\n2\n3\r\n"},
		{"unicode", "γειά 日本語 🐘\n"},
		{"block_crossing", strings.Repeat("b", 15) + "\nend"},
		{"partial_crossing", strings.Repeat("p", 16377) + "\nend"},
	} {
		for _, unicode := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/unicode_%v/%s", tc.name, unicode, symRouteName(prepared)), func(t *testing.T) {
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
						options := "s2k-count=1024,convert-crlf=1,unicode-mode=0"
						format := byte('t')
						if unicode {
							options = "s2k-count=1024,convert-crlf=1,unicode-mode=1"
							format = 'u'
						}
						wire := []byte(strings.ReplaceAll(tc.input, "\n", "\r\n"))
						packet := symEncrypt(t, p, c, prepared, false, []byte(tc.input), "κλειδί🔑", &options, symDefault(), format, wire, 1024)
						symDecrypt(t, p, c, r, packet, "κλειδί🔑", wire, false, &options)
						symDecrypt(t, p, c, r, packet, "κλειδί🔑", wire, true, nil)
						symDecrypt(t, p, c, r, packet, "κλειδί🔑", []byte(tc.input), true, &options)
						binary := symEncrypt(t, p, c, prepared, true, []byte(tc.input), "κλειδί🔑", &options, symDefault(), 'b', []byte(tc.input), 1024)
						symDecrypt(t, p, c, r, binary, "κλειδί🔑", []byte(tc.input), false, &options)
						if tc.name == "official" {
							pgpBoth(t, p, c, r, fixture.Select("SELECT encode(pgp_sym_decrypt_bytea($1::bytea,$2::text),'hex') AS octets", []string{"octets"}, []string{"TEXT"}, [][]any{{"310d0a320d0a330d0d0a"}}, packet, "κλειδί🔑"))
						}
					})
				})
			}
		}
	}
	for _, tc := range []struct{ name, inner, want string }{
		{"block_split", string(pgpRFCLiteral(strings.Repeat("a", 15)+"\r\nz\r", "partial", 't')), strings.Repeat("a", 15) + "\nz\r"},
		{"partial_split", string(pgpRFCLiteral(strings.Repeat("a", 16377)+"\r\nz\r", "partial", 'u')), strings.Repeat("a", 16377) + "\nz\r"},
		// Conversion state resets per literal: do not collapse across packets.
		{"literal_reset", string(append(pgpRFCLiteral("a\r", "", 't'), pgpRFCLiteral("\nb", "", 'u')...)), "a\r\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				packet := symBuild("convert", []byte(tc.inner), symDefault(), "")
				options := "convert-crlf=1"
				symDecrypt(t, p, c, r, packet, "convert", []byte(tc.want), true, &options)
			})
		})
	}
}
