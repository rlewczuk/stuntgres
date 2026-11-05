package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPSymmetricFeatureSmoke(t *testing.T) {
	symAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		input := make([]byte, 256)
		for i := range input {
			input[i] = byte(i)
		}
		profile := symProfile{cipher: 9, wrap: 7, digest: 8, mode: 3, session: true, compression: 2}
		options := symOptions(profile)
		for _, prepared := range []bool{false, true} {
			packet := symEncrypt(t, p, c, prepared, true, input, "smoke-password", &options, profile, 'b', input, 1024)
			symDecrypt(t, p, c, r, packet, "smoke-password", input, false, nil)
			pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt_bytea($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "incorrect")
			symDecrypt(t, p, c, r, symBase64(t, symZIP64), "key", []byte("Secret message"), true, nil)
			symDecrypt(t, p, c, r, symBase64(t, symZIP64), "key", []byte("Secret message"), false, nil)
			convert := "unicode-mode=1,convert-crlf=1,s2k-count=1024"
			converted := symEncrypt(t, p, c, prepared, false, []byte("1\n2\n3\r\n"), "smoke-password", &convert, symDefault(), 'u', []byte("1\r\n2\r\n3\r\r\n"), 1024)
			pgpBoth(t, p, c, r, fixture.Select("SELECT encode(pgp_sym_decrypt_bytea($1::bytea,$2::text),'hex') AS octets", []string{"octets"}, []string{"TEXT"}, [][]any{{"310d0a320d0a330d0d0a"}}, converted, "smoke-password"))
			symDecrypt(t, p, c, r, converted, "smoke-password", []byte("1\n2\n3\r\n"), true, &convert)
			inner := pgpRFCLiteral("\xff", "", 't')
			badText := symBuild("smoke-password", inner, symDefault(), "")
			d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`}
			symError(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", d, badText, "smoke-password")
			symDecrypt(t, p, c, r, badText, "smoke-password", []byte{255}, false, nil)
			pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", symBuild("smoke-password", inner, symDefault(), "mdc"), "smoke-password")
			noMDC := symDefault()
			noMDC.noMDC = true
			symDecrypt(t, p, c, r, symBuild("smoke-password", pgpRFCLiteral("boundary16bytes!x", "", 't'), noMDC, ""), "smoke-password", []byte("boundary16bytes!x"), true, nil)
			pgpBoth(t, p, c, r, fixture.Select("SELECT pgp_sym_encrypt_bytea($1::bytea,$2::text,$3::text)", []string{"pgp_sym_encrypt_bytea"}, []string{"BYTEA"}, [][]any{{nil}}, nil, "smoke-password", "unknown=1"))
		}
	})
}
