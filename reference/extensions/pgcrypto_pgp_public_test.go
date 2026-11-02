package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPVector(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		packet := pgpVector(t)
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Secret message.", packet, "foobar"))
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "Secret message.", packet, "foobar", ""))
		// Literal SQL as well as bound/prepared values, without dearmor.
		q := fmt.Sprintf("SELECT pgp_sym_decrypt('\\x%x'::bytea,'foobar')", packet)
		pgpBoth(t, p, c, r, pgpTextStep(q, "Secret message."))
	})
}

func TestReferencePgcryptoPGPPublic(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		profile := pgpProfile
		for _, prepared := range []bool{false, true} {
			for _, options := range []*string{nil, &profile} {
				// Independently authenticate random output, never compare ciphertext.
				for range 2 {
					count := 0
					if options != nil {
						count = 65536
					}
					packet := pgpEncryptCheck(t, p, c, prepared, "Hôtel 東京🙂\nline\r\n", "päss 東京", options, count)
					pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Hôtel 東京🙂\nline\r\n", packet, "päss 東京"))
					pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "Hôtel 東京🙂\nline\r\n", packet, "päss 東京", "convert-crlf=0"))
				}
			}
		}
		// No synthetic substrate SQL is needed for the public packet path.
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt(pgp_sym_encrypt('literal','foobar'),'foobar')", "literal"))
	})
}

func TestReferencePgcryptoPGPFraming(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		options := "s2k-count=1024"
		for _, n := range []int{0, 1, 142, 143, 185, 186, 8333, 8334, 8377, 8378, 16333, 16334, 16377, 16378, 32768} {
			t.Run(fmt.Sprintf("emitted_literal_body_%d", n+6), func(t *testing.T) {
				text := strings.Repeat("x", n)
				for _, prepared := range []bool{false, true} {
					packet := pgpEncryptCheck(t, p, c, prepared, text, "foobar", &options, 1024)
					pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", text, packet, "foobar"))
				}
			})
		}
		// Authenticated inner and outer body boundaries, including two complete
		// partial chunks followed by a required definite terminal length.
		for _, form := range []string{"", "old1", "old2", "old4", "five", "partial"} {
			for _, n := range []int{0, 142, 143, 185, 186, 8333, 8334, 8377, 8378, 16333, 16334, 16378, 32768} {
				if form == "old1" && n > 100 {
					continue
				}
				t.Run(fmt.Sprintf("authored_%s_%d", form, n), func(t *testing.T) {
					text := strings.Repeat("z", n)
					packet := pgpRFCBuild("foobar", pgpRFCLiteral(text, form, 't'), form, "")
					d, err := pgpRFCDecode(packet, "foobar")
					if err != nil || d.text != text {
						t.Fatal("independent builder anchor", err)
					}
					pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", text, packet, "foobar"))
				})
			}
		}
		// Longer-than-count salt/password unit still hashes one whole unit.
		password := strings.Repeat("p", 1100)
		packet := pgpEncryptCheck(t, p, c, false, "long password", password, &options, 1024)
		pgpBoth(t, p, c, r, pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "long password", packet, password))
		// Huge attacker declarations with short actual bodies must not drive
		// allocation; independent framing rejects these before backend use.
		for _, packet := range [][]byte{
			{195, 255, 255, 255, 255, 255}, {140, 13, 4}, {195, 192}, {195, 255, 0, 0}, {210, 238, 1},
			append([]byte{195, 238}, bytes.Repeat([]byte{0}, 16384)...),
		} {
			if _, _, err := pgpRFCRead(packet, false); err == nil {
				t.Fatal("independent reader accepted truncated framing")
			}
			for _, prepared := range []bool{false, true} {
				pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "foobar")
			}
		}
	})
}
