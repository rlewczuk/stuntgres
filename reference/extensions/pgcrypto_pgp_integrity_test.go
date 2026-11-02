package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPIntegrity(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		password := "pgp-private-password"
		literal := pgpRFCLiteral("PGP confidential sentinel", "", 't')
		valid := pgpRFCBuild(password, literal, "", "")
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "PGP confidential sentinel", valid, password))
		flip := append([]byte(nil), valid...)
		flip[len(flip)-1] ^= 1
		badVersion := append([]byte(nil), valid...)
		// Independent key frame is old1, total 15 bytes; SEIPD one-octet
		// length follows its tag. Mutate the clear version, not plaintext.
		badVersion[17] = 2
		badMode := append([]byte(nil), valid...)
		badMode[4] = 2
		badCipher := append([]byte(nil), valid...)
		badCipher[3] = 127
		badDigest := append([]byte(nil), valid...)
		badDigest[5] = 127
		keyFrame, rest, err := pgpRFCRead(valid, false)
		if err != nil {
			t.Fatal(err)
		}
		duplicate := append(pgpRFCFrame(3, keyFrame.body, "old1"), valid...)
		for _, tc := range []struct {
			name          string
			packet        []byte
			pass, message string
		}{
			{"wrong_password", valid, "wrong", "Wrong key or corrupt data"},
			{"ciphertext_flip", flip, password, "Wrong key or corrupt data"},
			{"truncated_mdc", valid[:len(valid)-1], password, "Wrong key or corrupt data"},
			{"missing_mdc", pgpRFCBuild(password, literal, "", "missing_mdc"), password, "Wrong key or corrupt data"},
			{"prefix", pgpRFCBuild(password, literal, "", "prefix"), password, "Wrong key or corrupt data"},
			{"version", badVersion, password, "Wrong key or corrupt data"},
			{"s2k_mode", badMode, password, "Bad S2K mode"},
			// Packet algorithm IDs are dispatch inputs, never AES128 fallbacks.
			// Pinned 18.6 reports corrupt data for unknown digest packet IDs;
			// this differs from option-name lookup's unsupported-digest error.
			{"cipher_id", badCipher, password, "Unsupported cipher algorithm"},
			{"digest_id", badDigest, password, "Wrong key or corrupt data"},
			{"no_key", rest, password, "Wrong key or corrupt data"},
			{"key_only", pgpRFCFrame(3, keyFrame.body, "old1"), password, "Wrong key or corrupt data"},
			{"duplicate_key", duplicate, password, "Wrong key or corrupt data"},
			{"trailing_garbage", append(append([]byte(nil), valid...), 0), password, "Wrong key or corrupt data"},
			{"empty", []byte{}, password, "Wrong key or corrupt data"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", tc.message, tc.packet, tc.pass)
				}
			})
		}
		for _, tc := range []struct {
			name    string
			inner   []byte
			message string
		}{
			{"binary_literal", pgpRFCLiteral("PGP confidential sentinel", "", 'b'), "Not text data"},
			{"unsupported_compression", pgpRFCFrame(8, []byte{3, 0}, ""), "Unsupported compression algorithm"},
			{"truncated_filename", pgpRFCFrame(11, []byte{'t', 255, 0, 0, 0, 0}, ""), "Wrong key or corrupt data"},
			{"truncated_timestamp", pgpRFCFrame(11, []byte{'t', 0, 0}, ""), "Wrong key or corrupt data"},
			{"missing_literal", nil, "Wrong key or corrupt data"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, corruption := range []string{"", "mdc"} {
					message := tc.message
					if corruption != "" {
						message = "Wrong key or corrupt data"
					}
					packet := pgpRFCBuild(password, tc.inner, "", corruption)
					for _, prepared := range []bool{false, true} {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", message, packet, password)
					}
				}
			})
		}
		// Recover on the same physical borrower after failed producers.
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Secret message.", pgpVector(t), "foobar"))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		pgpNoLeak(t, p, c, false, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", flip, password)
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Secret message.", pgpVector(t), "foobar"))
	})
}
