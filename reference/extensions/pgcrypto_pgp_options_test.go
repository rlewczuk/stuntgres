package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPOptions(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, tc := range []struct{ name, options, message string }{
			{"unknown", "unknown=1", "Illegal argument to function"},
			{"missing_equals", "cipher-algo aes128", "Illegal argument to function"},
			{"missing_value", "cipher-algo=", "Illegal argument to function"},
			{"semicolon", "cipher-algo=aes128;s2k-mode=3", "Illegal argument to function"},
			{"semicolon_cipher_name", "cipher-algo=aes128;foo", "Unsupported cipher algorithm"},
			{"count_low", "s2k-count=1023", "Illegal argument to function"},
			{"count_high", "s2k-count=65011713", "Illegal argument to function"},
			{"mode", "s2k-mode=2", "Illegal argument to function"},
			{"compress", "compress-algo=4", "Illegal argument to function"},
			{"level", "compress-level=10", "Illegal argument to function"},
			{"negative_level", "compress-level=-1", "Illegal argument to function"},
			{"cipher", "cipher-algo=nope", "Unsupported cipher algorithm"},
			{"s2k_cipher", "s2k-cipher-algo=nope", "Unsupported cipher algorithm"},
			{"digest", "s2k-digest-algo=nope", "Unsupported digest algorithm"},
			{"encounter_mode_count", "s2k-mode=0,s2k-count=1024,s2k-mode=3", "Illegal argument to function"},
			{"invalid_not_cured", "s2k-count=1023,s2k-count=1024", "Illegal argument to function"},
			// Source-derived parser edge goldens confirmed by root's pinned
			// PostgreSQL 18.6 checkpoint before production implementation.
			{"whitespace_only", " \t\n", "Illegal argument to function"},
			{"leading_comma", ",s2k-count=1024", "Illegal argument to function"},
			{"double_comma", "s2k-count=1024,,compress-algo=0", "Illegal argument to function"},
			{"carriage_return_key", "\rcipher-algo=aes128", "Illegal argument to function"},
			{"carriage_return_value", "cipher-algo=aes128\r", "Unsupported cipher algorithm"},
			{"count_c_int_max", "s2k-count=2147483647", "Illegal argument to function"},
			{"count_c_int_min", "s2k-count=-2147483648", "Illegal argument to function"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_encrypt($1::text,$2::text,$3::text)", tc.message, "PGP confidential sentinel", "pgp-private-password", tc.options)
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", tc.message, pgpVector(t), "foobar", tc.options)
					// Option validation precedes packet interpretation.
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", tc.message, []byte{0}, "foobar", tc.options)
				}
			})
		}
		for _, tc := range []struct {
			name, options string
			count         int
		}{
			{"empty", "", 0},
			{"defaults", pgpProfile + ",compress-level=6,disable-mdc=0,sess-key=0,unicode-mode=0,convert-crlf=0,ignore-cipher-failure=0", 65536},
			{"count_min", "s2k-count=1024", 1024},
			{"count_round", "s2k-count=1025", 1088},
			{"count_max", "s2k-count=65011712", 65011712},
			{"trailing_comma", "s2k-count=1024,", 1024},
			{"carriage_return_numeric", "s2k-count=1024,compress-algo=0\r", 1024},
			{"case_space", " CIPHER-ALGO \t= AES128 ,\n S2K-COUNT = 1024", 1024},
			{"last_wins", "cipher-algo=aes256,cipher-algo=aes128,s2k-count=2048,s2k-count=1024", 1024},
			{"mode_override", "s2k-count=1024,s2k-mode=0,s2k-mode=3", 1024},
			{"numeric_prefix", "s2k-count=1024junk,compress-algo=nonnumeric,disable-mdc=nonnumeric,sess-key=0tail", 1024},
			{"explicit_s2k_cipher", "s2k-cipher-algo=aes128,s2k-count=1024", 1024},
			{"compression_level_irrelevant", "compress-algo=0,compress-level=9,s2k-count=1024", 1024},
			{"nonzero_boolean", "ignore-cipher-failure=-2,s2k-count=1024", 1024},
			{"boolean_c_int_max", "ignore-cipher-failure=2147483647,s2k-count=1024", 1024},
			{"boolean_c_int_min", "ignore-cipher-failure=-2147483648,s2k-count=1024", 1024},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					packet := pgpEncryptCheck(t, p, c, prepared, "option text", "foobar", &tc.options, tc.count)
					pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "option text", packet, "foobar", tc.options))
				}
			})
		}
		// Valid encryption preferences do not override actual packet algorithms.
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "Secret message.", pgpVector(t), "foobar", "cipher-algo=aes256,s2k-mode=0,s2k-digest-algo=sha256,compress-algo=2,disable-mdc=1,sess-key=1"))
		for _, prepared := range []bool{false, true} {
			pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_encrypt($1::text,$2::text)", "Illegal argument to function", "PGP confidential sentinel", "")
			pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Illegal argument to function", pgpVector(t), "")
		}
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Secret message.", pgpVector(t), "foobar"))
	})
}
