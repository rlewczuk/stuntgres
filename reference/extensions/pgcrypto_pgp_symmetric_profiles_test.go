package reference_test

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Every fixed ciphertext below is from REL_18_6 contrib/pgcrypto/sql/pgp-decrypt.sql;
// plaintext and binary SHA1 are published in expected/pgp-decrypt.out. None is
// learned from either backend. The upstream label does not imply GnuPG origin.
func TestReferencePgcryptoPGPSymmetricVectors(t *testing.T) {
	symAnchor(t)
	for _, tc := range []struct{ name, encoded string }{
		{"bf", "jA0EBAMCfFNwxnvodX9g0jwB4n4s26/g5VmKzVab1bX1SmwY7gvgvlWdF3jKisvSyA6Ce1QTMK3KdL2MPfamsTUSAML8huCJMwYQFfE="},
		{"aes128", pgpVector64},
		{"aes192", "jA0ECAMCI7YQpWqp3D1g0kQBCjB7GlX7+SQeXNleXeXQ78ZAPNliquGDq9u378zI5FPTqAhIB2/2fjY8QEIs1ai00qphjX2NitxV/3Wn+6dufB4Q4g=="},
		{"aes256", "jA0ECQMC4f/5djqCC1Rg0kQBTHEPsD+Sw7biBsM2er3vKyGPAQkuTBGKC5ie7hT/lceMfQdbAg6oTFyJpk/wH18GzRDphCofg0X8uLgkAKMrpcmgog=="},
		{"nomdc", symNoMDC64},
		{"md5", "jA0EBwMClrXXtOXetohg0kQBn0Kl1ymevQZRHkdoYRHgzCwSQEiss7zYff2UNzgOKyRrHf7zEBuZiZ2AG34jNVMOLToj1jJUg5zTSdecUzQVCykWTA=="},
		{"sha1", "jA0EBwMCApbdlrURoWJg0kQBzHM/E0o7djY82bNuspjxjAcPFrrtp0uvDdMQ4z2m/PM8jhgI5vxFYfNQjLl8y3fHYIomk9YflN9K/Q13iq8A8sjeTw=="},
		{"aes128_mode0", "jAQEBwAC0kQBKTaLAKE3xzps+QIZowqRNb2eAdzBw2LxEW2YD5PgNlbhJdGg+dvwAh9GXjGS1TVALzTImJbz1uHUZRfhJlFbc5yGQw=="},
		{"aes128_mode1", "jAwEBwEC/QTByBLI3b/SRAHPxKzI6SZBo5lAEOD+EsvKQWO4adL9tDY+++Iqy1xK4IaWXVKEj9R2Lr2xntWWMGZtcKtjD2lFFRXXd9dZp1ZThNDz"},
		{"aes128_mode3", "jA0EBwMCEq4Su3ZqNEJg0kQB4QG5jBTKF0i04xtH+avzmLhstBNRxvV3nsmB3cwlz+9ZaA/XdSx5ZiFnMym8P6r8uY9rLjjNptvvRHlxIReF+p9MNg=="},
		{"aes192_mode0", "jAQECAAC0kQBBDnQWkgsx9YFaqDfWmpsiyAJ6y2xG/sBvap1dySYEMuZ+wJTXQ9ECr3i2M7TgVZ0M4jp4QL0adG1lpN5iK7aQeOwMw=="},
		{"aes192_mode1", "jAwECAECruOfyNDFiTnSRAEVoGXm4A9UZKkWljdzjEO/iaE7mIraltIpQMkiqCh97h8uZ2u9uRBOv222fZodGvc6bvq/4R4hAa/6qSHtm8mdmvGt"},
		{"aes192_mode3", "jA0ECAMCjFn6SRi3SONg0kQBqtSHPaD0m7rXfDAhCWU/ypAsI93GuHGRyM99cvMvq6eF6859ZVnli3BFSDSk3a4e/pXhglxmDYCfjAXkozKNYLo6yw=="},
		{"aes256_mode0", "jAQECQAC0kQB4L1eMbani07XF2ZYiXNK9LW3v8w41oUPl7dStmrJPQFwsdxmrDHurQr3WbdKdY9ufjOE5+mXI+EFkSPrF9rL9NCq6w=="},
		{"aes256_mode1", "jAwECQECKHhrou7ZOIXSRAHWIVP+xjVQcjAVBTt+qh9SNzYe248xFTwozkwev3mO+KVJW0qhk0An+Y2KF99/bYFl9cL5D3Tl43fC8fXGl3x3m7pR"},
		{"aes256_mode3", "jA0ECQMCjc8lwZu8Fz1g0kQBkEzjImi21liep5jj+3dAJ2aZFfUkohi8b3n9z+7+4+NRzL7cMW2RLAFnJbiqXDlRHMwleeuLN1up2WIxsxtYYuaBjA=="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := symBase64(t, tc.encoded)
			d, err := symDecode(packet, "foobar")
			if err != nil || string(d.data) != "Secret message." {
				t.Fatal("official vector anchor", err)
			}
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				symDecrypt(t, p, c, r, packet, "foobar", []byte("Secret message."), true, nil)
				symDecrypt(t, p, c, r, packet, "foobar", []byte("Secret message."), false, nil)
			})
		})
	}
	t.Run("official_zip", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			symDecrypt(t, p, c, r, symBase64(t, symZIP64), "key", []byte("Secret message"), true, nil)
		})
	})
	t.Run("official_binary_long_password", func(t *testing.T) {
		packet := symBase64(t, "jA0EBwMCxQvxJZ3G/HRg0lgBeYmTa7/uDAjPyFwSX4CYBgpZWVn/JS8JzILrcWF8gFnkUKIE0PSaYFp+Yi1VlRfUtRQ/X/LYNGa7tWZS+4VQajz2Xtz4vUeAEiYFYPXk73Hb8m1yRhQK")
		d, err := symDecode(packet, "0123456789abcdefghij")
		sum := sha1.Sum(d.data)
		if err != nil || hex.EncodeToString(sum[:]) != "5e5c135efc0dd00633efc6dfd6e731ea408a5b4c" {
			t.Fatal("published binary SHA1 anchor", err)
		}
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			symDecrypt(t, p, c, r, packet, "0123456789abcdefghij", d.data, false, nil)
			pgpBoth(t, p, c, r, fixture.Select("SELECT encode(digest(pgp_sym_decrypt_bytea($1::bytea,$2::text),'sha1'),'hex') AS digest", []string{"digest"}, []string{"TEXT"}, [][]any{{"5e5c135efc0dd00633efc6dfd6e731ea408a5b4c"}}, packet, "0123456789abcdefghij"))
		})
	})
}

func TestReferencePgcryptoPGPSymmetricProfiles(t *testing.T) {
	symAnchor(t)
	profiles := []struct {
		name string
		p    symProfile
	}{
		{"aes128_md5_mode0", symProfile{cipher: 7, wrap: 7, digest: 1, mode: 0}},
		{"cast5_sha256_mode1", symProfile{cipher: 3, wrap: 3, digest: 8, mode: 1}},
		{"bf_sha1_mode3", symProfile{cipher: 4, wrap: 4, digest: 2, mode: 3}},
		{"3des_md5_multihash", symProfile{cipher: 2, wrap: 2, digest: 1, mode: 3}},
		{"aes192_sha1_multihash", symProfile{cipher: 8, wrap: 8, digest: 2, mode: 3}},
		{"aes256_sha1_multihash", symProfile{cipher: 9, wrap: 9, digest: 2, mode: 3}},
		{"aes256_sha256", symProfile{cipher: 9, wrap: 9, digest: 8, mode: 3}},
		{"aes256_wrapped_bf", symProfile{cipher: 9, wrap: 4, digest: 8, mode: 3, session: true}},
		{"bf_wrapped_aes256", symProfile{cipher: 4, wrap: 9, digest: 2, mode: 1, session: true}},
		{"3des_wrapped_cast5", symProfile{cipher: 2, wrap: 3, digest: 1, mode: 0, session: true}},
		{"cast5_wrapped_3des", symProfile{cipher: 3, wrap: 2, digest: 2, mode: 3, session: true}},
		{"aes128_session", symProfile{cipher: 7, wrap: 7, digest: 8, mode: 3, session: true}},
	}
	for _, tc := range profiles {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					input := []byte("profile π confidential witness")
					options := symOptions(tc.p)
					packet := symEncrypt(t, p, c, prepared, false, input, "profile-password", &options, tc.p, 't', input, 1024)
					symDecrypt(t, p, c, r, packet, "profile-password", input, true, nil)
					// A separately RFC-authored packet is a second direction of interoperability.
					authored := symBuild("profile-password", pgpRFCLiteral(string(input), "partial", 'u'), tc.p, "")
					symDecrypt(t, p, c, r, authored, "profile-password", input, true, nil)
				})
			})
		}
	}
	for _, tc := range []struct {
		name, options string
		p             symProfile
		count         int
		password      string
	}{
		{"default", "", symDefault(), 0, "password"},
		{"explicit_default", "s2k-count=1024", symDefault(), 1024, "password"},
		{"aes_alias", "cipher-algo=aes,s2k-digest-algo=sha-1,s2k-count=1024", symDefault(), 1024, "password"},
		{"blowfish_alias", "cipher-algo=blowfish,s2k-count=1024", symProfile{cipher: 4, wrap: 4, digest: 2, mode: 3}, 1024, "password"},
		{"round_up", "s2k-count=1025", symDefault(), 1088, "password"},
		{"count_65536", "s2k-count=65536", symDefault(), 65536, "password"},
		{"count_max", "s2k-count=65011712", symDefault(), 65011712, "password"},
		{"password_unit_exceeds_count", "s2k-count=1024", symDefault(), 1024, strings.Repeat("long-password-π", 100)},
		// OpenSSL's generic AES chooses strength from the S2K key length with sess-key=0.
		{"mixed_aes128_wrap256", "cipher-algo=aes128,s2k-cipher-algo=aes256,s2k-count=1024", symProfile{cipher: 9, wrap: 9, digest: 2, mode: 3}, 1024, "password"},
		{"mixed_aes256_wrap128", "cipher-algo=aes256,s2k-cipher-algo=aes128,s2k-count=1024", symDefault(), 1024, "password"},
		{"last_duplicate", "cipher-algo=aes256,cipher-algo=cast5,s2k-count=2048,s2k-count=1024", symProfile{cipher: 3, wrap: 3, digest: 2, mode: 3}, 1024, "password"},
		{"case_whitespace", " CIPHER-ALGO\t= AES256 ,\n S2K-COUNT = 1024,", symProfile{cipher: 9, wrap: 9, digest: 2, mode: 3}, 1024, "password"},
		{"numeric_prefix", "s2k-count=1024junk,sess-key=nonnumeric,disable-mdc=nonnumeric,compress-algo=0\r", symDefault(), 1024, "password"},
		{"count_before_mode", "s2k-count=1024,s2k-mode=0,s2k-mode=3", symDefault(), 1024, "password"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s", tc.name, symRouteName(prepared)), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					input := []byte("S2K boundary")
					packet := symEncrypt(t, p, c, prepared, false, input, tc.password, &tc.options, tc.p, 't', input, tc.count)
					symDecrypt(t, p, c, r, packet, tc.password, input, true, nil)
				})
			})
		}
	}
	symMixedProfiles(t)
}
