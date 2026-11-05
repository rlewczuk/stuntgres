package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Explicitly decode an emission whose selected data cipher differs from its
// SKESK algorithm with sess-key=0. This is not a fallback in symDecode: normal
// OpenPGP decryption must still follow the SKESK and fail authentication here.
// All complete framing, prefix, SHA1 MDC and literal checks are shared unchanged.
func symDecodeMixed(packet []byte, password string, dataCipher, wrap byte) (symDecoded, error) {
	var d symDecoded
	keyPacket, rest, err := pgpRFCRead(packet, false)
	if err != nil {
		return d, err
	}
	k := keyPacket.body
	if keyPacket.tag != 3 || len(k) != 13 || !bytes.Equal(k[:4], []byte{4, wrap, 3, 8}) || k[12] != 0 {
		return d, fmt.Errorf("mixed fixture needs exact direct SHA256/count1024 SKESK")
	}
	derived := symKey(password, k[4:12], 3, 8, 1024, symKeySize(wrap))
	defer clear(derived)
	dataKey := derived
	// REL_18_6 contrib/pgcrypto/openssl.c: BF preserves the supplied key
	// length; DES3 explicitly zero-pads/truncates to24; AES ECB strength is
	// selected from supplied key length, regardless of the data selector name.
	// OpenSSL3.0.19 CAST provider uses variable key length, then CAST_set_key
	// in crypto/cast/c_skey.c clamps lengths above16 to16. The x/crypto test
	// primitive receives that independently specified effective16-byte key.
	switch dataCipher {
	case 2:
		dataKey = make([]byte, 24)
		copy(dataKey, derived)
		defer clear(dataKey)
	case 3:
		dataKey = derived[:16]
	case 4:
		// Blowfish deliberately uses the entire16/24/32-byte S2K key.
	case 7, 8, 9:
		switch len(derived) {
		case 16:
			dataCipher = 7
		case 24:
			dataCipher = 8
		case 32:
			dataCipher = 9
		default:
			return d, fmt.Errorf("mixed AES key size")
		}
	default:
		return d, fmt.Errorf("mixed data cipher")
	}
	block, err := symBlock(dataCipher, dataKey)
	if err != nil {
		return d, err
	}
	d.profile = symProfile{cipher: dataCipher, wrap: wrap, digest: 8, mode: 3}
	d.keySize = len(dataKey)
	d.saltSize = 8
	d.count = 1024
	return symDecodeData(rest, block, d)
}

// Source-derived expected provider behavior, observed only when root runs the
// pinned PostgreSQL fixture. Ordinary header-driven decrypt is independently
// expected corrupt: the SKESK records the S2K selector, not the actual data one.
// Source references:
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pgcrypto/openssl.c
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pgcrypto/pgp-encrypt.c
// https://raw.githubusercontent.com/openssl/openssl/openssl-3.0.19/providers/implementations/ciphers/cipher_cast5.c
// https://raw.githubusercontent.com/openssl/openssl/openssl-3.0.19/providers/implementations/ciphers/ciphercommon.c
// https://raw.githubusercontent.com/openssl/openssl/openssl-3.0.19/providers/implementations/ciphers/cipher_cast5_hw.c
// https://raw.githubusercontent.com/openssl/openssl/openssl-3.0.19/crypto/cast/c_skey.c
func symMixedProfiles(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, dataName, wrapName string
		data, wrap, effective    byte
		keySize                  int
	}{
		{"bf_wrap_aes256", "bf", "aes256", 4, 9, 4, 32},
		{"3des_wrap_aes128", "3des", "aes128", 2, 7, 2, 24},
		{"3des_wrap_aes256", "3des", "aes256", 2, 9, 2, 24},
		{"cast5_wrap_aes192", "cast5", "aes192", 3, 8, 3, 16},
		{"cast5_wrap_aes256", "cast5", "aes256", 3, 9, 3, 16},
		{"aes128_wrap_3des", "aes128", "3des", 7, 2, 8, 24},
		{"aes128_wrap_bf", "aes128", "bf", 7, 4, 7, 16},
	} {
		for _, binary := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("mixed_direct_key/%s/binary_%v/%s", tc.name, binary, symRouteName(prepared)), func(t *testing.T) {
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
						name, first := "pgp_sym_encrypt", "text"
						input := []byte("mixed direct-key provider semantics")
						var value any = string(input)
						format := byte('t')
						if binary {
							name, first = "pgp_sym_encrypt_bytea", "bytea"
							input = []byte{0, 255, 13, 10, 128, 65}
							value = input
							format = 'b'
						}
						options := fmt.Sprintf("cipher-algo=%s,s2k-cipher-algo=%s,sess-key=0,s2k-mode=3,s2k-digest-algo=sha256,s2k-count=1024,compress-algo=0", tc.dataName, tc.wrapName)
						rows := pgpQuery(t, p, c, prepared, "SELECT "+name+"($1::"+first+",$2::text,$3::text)", value, "mixed-password", options)
						defer rows.Close()
						metadata := pgpMetadata(t, rows, name, "BYTEA", false, false)
						if !rows.Next() {
							t.Fatal("mixed encryption missing row", rows.Err())
						}
						var packet []byte
						if err := rows.Scan(&packet); err != nil {
							t.Fatal(err)
						}
						d, err := symDecodeMixed(packet, "mixed-password", tc.data, tc.wrap)
						if err != nil {
							t.Fatal("independent mixed-selector authentication", err)
						}
						want := symProfile{cipher: tc.effective, wrap: tc.wrap, digest: 8, mode: 3}
						if d.profile != want || d.keySize != tc.keySize || d.saltSize != 8 || d.count != 1024 || d.filenameLength != 0 || !bytes.Equal(d.formats, []byte{format}) || !bytes.Equal(d.data, input) {
							t.Fatal("mixed-selector packet/profile/literal differs from independent expectation")
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("mixed encryption row tail", rows.Err())
						}
						pgpColumns(t, metadata, name, "BYTEA", false, false)
						if _, err := rows.ColumnTypes(); err == nil || err.Error() != "sql: Rows are closed" {
							t.Fatal("mixed closed ColumnTypes", err)
						}
						if _, err := rows.Columns(); err == nil || err.Error() != "sql: Rows are closed" {
							t.Fatal("mixed closed Columns", err)
						}
						if _, err := symDecode(packet, "mixed-password"); err == nil {
							t.Fatal("normal header-driven oracle incorrectly authenticated mixed packet")
						}
						badMDC := append([]byte(nil), packet...)
						badMDC[len(badMDC)-1] ^= 1
						if _, err := symDecodeMixed(badMDC, "mixed-password", tc.data, tc.wrap); err == nil {
							t.Fatal("explicit mixed decoder failed to enforce MDC")
						}
						for _, decrypt := range []string{"pgp_sym_decrypt", "pgp_sym_decrypt_bytea"} {
							for _, decryptPrepared := range []bool{false, true} {
								pgpNoLeak(t, p, c, decryptPrepared, "SELECT "+decrypt+"($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "mixed-password")
								// Supplying original preferences cannot override packet dispatch.
								pgpNoLeak(t, p, c, decryptPrepared, "SELECT "+decrypt+"($1::bytea,$2::text,$3::text)", "Wrong key or corrupt data", packet, "mixed-password", options)
							}
						}
					})
				})
			}
		}
	}
}
