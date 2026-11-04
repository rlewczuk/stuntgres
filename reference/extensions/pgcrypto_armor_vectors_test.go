package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoArmorVectors(t *testing.T) {
	for _, vector := range []struct {
		name, data, packet string
	}{
		{"empty", "", pgcryptoArmorEmpty},
		{"test", "test", pgcryptoArmorTest},
		{"rfc_crc24", "123456789", pgcryptoArmorDigits},
		{"multiline", "0123456789abcdef0123456789abcdef0123456789abcdef\n0123456789abcdef0123456789abcdef0123456789abcdef", pgcryptoArmorLong},
	} {
		t.Run(vector.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1)", vector.packet, []byte(vector.data)))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1::bytea)", vector.packet, []byte(vector.data)))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", []byte(vector.data), vector.packet))
			})
		})
	}
	t.Run("header_vector", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1,$2,$3)", fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"))
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", []byte("zooka"), fixture.PgcryptoArmorZooka))
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor('test')", pgcryptoArmorTest))
		})
	})
}

func TestReferencePgcryptoArmorBoundaries(t *testing.T) {
	// These literal bodies/checksums were independently authored with RFC 4880
	// Base64/bitwise CRC24, anchored by empty/test/123456789, not a native codec.
	for _, vector := range []struct {
		n         int
		body, crc string
	}{
		{0, "", "twTO"},
		{1, "YQ==", "8lcT"},
		{2, "YWE=", "rYPb"},
		{3, "YWFh", "m1iu"},
		{56, "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=", "MoHs"},
		{57, "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh", "UZit"},
		{58, "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh\nYQ==", "UTJg"},
		{114, "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh\nYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh", "rgvu"},
		{115, "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh\nYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFh\nYQ==", "H/RY"},
	} {
		t.Run(fmt.Sprintf("bytes_%d", vector.n), func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				body := vector.body
				if body != "" {
					body += "\n"
				}
				packet := "-----BEGIN PGP MESSAGE-----\n\n" + body + "=" + vector.crc + "\n-----END PGP MESSAGE-----\n"
				data := []byte(strings.Repeat("a", vector.n))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1::bytea)", packet, data))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", data, packet))
			})
		})
	}
	for _, vector := range []struct {
		name   string
		data   []byte
		packet string
	}{
		{"nul_high_bit_backslash", []byte{0, 1, 127, 128, 255, 92}, "-----BEGIN PGP MESSAGE-----\n\nAAF/gP9c\n=TG7N\n-----END PGP MESSAGE-----\n"},
		{"all_octets", func() []byte {
			b := make([]byte, 256)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(), "-----BEGIN PGP MESSAGE-----\n\nAAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4\nOTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2RlZmdoaWprbG1ub3Bx\ncnN0dXZ3eHl6e3x9fn+AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeYmZqbnJ2en6ChoqOkpaanqKmq\nq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TFxsfIycrLzM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj\n5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+/w==\n=W700\n-----END PGP MESSAGE-----\n"},
	} {
		t.Run(vector.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1)", vector.packet, vector.data))
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", vector.data, vector.packet))
			})
		})
	}
	for _, vector := range []struct{ name, packet string }{
		{"crlf", strings.ReplaceAll(pgcryptoArmorTest, "\n", "\r\n")},
		{"footer_no_lf", strings.TrimSuffix(pgcryptoArmorTest, "\n")},
		{"surrounding_text", "unrelated preamble\n" + pgcryptoArmorTest + "unrelated suffix"},
		{"body_whitespace", strings.Replace(pgcryptoArmorTest, "dGVzdA==", "dG Vz\tdA\r\n==", 1)},
		{"mismatched_labels", "-----BEGIN arbitrary-----\n\ndGVzdA==\n=+G7Q\n-----END different-----"},
		{"empty_labels", "-----BEGIN-----\n\ndGVzdA==\n=+G7Q\n-----END-----"},
		{"first_packet", pgcryptoArmorTest + fixture.PgcryptoArmorZooka},
		{"skip_indented_inline_markers", "x-----BEGIN PGP MESSAGE-----\n -----BEGIN PGP MESSAGE-----\n" + pgcryptoArmorTest},
		{"discarded_pad_bits", strings.Replace(pgcryptoArmorTest, "dGVzdA==", "dGVzdB==", 1)},
		{"alphabet_after_padding", strings.Replace(pgcryptoArmorTest, "dGVzdA==", "dGVzdA=A", 1)},
		{"crc_trailing_junk", strings.Replace(pgcryptoArmorTest, "=+G7Q\n", "=+G7Qjunk\n", 1)},
	} {
		t.Run(vector.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1)", []byte("test"), vector.packet))
			})
		})
	}
}
