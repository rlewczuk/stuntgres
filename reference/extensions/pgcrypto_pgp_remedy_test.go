package reference_test

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Do not let a failing large-fixture assertion print megabytes of plaintext or
// bound BYTEA. Still scan the complete public TEXT and retain exact metadata.
func pgpRemedyText(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, packet []byte, password, text string) {
	t.Helper()
	rows := pgpQuery(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", packet, password)
	defer rows.Close()
	metadata := pgpMetadata(t, rows, "pgp_sym_decrypt", "TEXT", false, false)
	if !rows.Next() {
		t.Fatal("missing complete literal result", rows.Err())
	}
	var got string
	if err := rows.Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Fatal("literal content mismatch; actual/expected byte lengths", len(got), len(text))
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("literal result tail", rows.Err())
	}
	pgpColumns(t, metadata, "pgp_sym_decrypt", "TEXT", false, false)
}

func pgpRemedyLength(t *testing.T, wire []byte, form string, bodyBytes int) {
	t.Helper()
	switch form {
	case "old4":
		if len(wire) != bodyBytes+5 || wire[0]&67 != 2 || int(binary.BigEndian.Uint32(wire[1:5])) != bodyBytes {
			t.Fatal("old-four actual length word/body mismatch")
		}
	case "five":
		if len(wire) != bodyBytes+6 || wire[1] != 255 || int(binary.BigEndian.Uint32(wire[2:6])) != bodyBytes {
			t.Fatal("new-five actual length word/body mismatch")
		}
	case "partial24":
		const size = 16 * 1024 * 1024
		if bodyBytes < size || bodyBytes > size+1 || len(wire) != bodyBytes+3 ||
			wire[1] != 248 || int(wire[2+size]) != bodyBytes-size {
			t.Fatal("16MiB partial plus definite terminal mismatch")
		}
	case "partial-terminal":
		const size = 16384
		if len(wire) != bodyBytes+7 || wire[1] != 238 || wire[2+size] != 255 ||
			int(binary.BigEndian.Uint32(wire[3+size:7+size])) != bodyBytes-size {
			t.Fatal("actual partial/terminal length words mismatch")
		}
	case "partial":
		if wire[1] != 238 {
			t.Fatal("large aggregate lacks small partial framing")
		}
	}
}

// REL_18_6 pgp-decrypt.c process_data_packets uses ALLOW_CTX_SIZE and appends
// successive literals. pgp_decrypt uses NO_CTX_SIZE outside authentication.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pgcrypto/pgp-decrypt.c
func TestReferencePgcryptoPGPAuthenticatedFraming(t *testing.T) {
	pgpRFCAnchor(t)
	const password = "pgp-private-password"
	partial := pgpRFCPartialFrame(11, append([]byte{'t', 0, 0, 0, 0, 0}, []byte("PGP confidential ")...), 3, 2)
	for _, tc := range []struct {
		name     string
		inner    []byte
		literals int
	}{
		{"old_context", pgpRFCLiteral("PGP confidential sentinel", "context", 't'), 1},
		{"multiple_definite", append(pgpRFCLiteral("PGP confidential ", "old1", 't'), pgpRFCLiteral("sentinel", "five", 'u')...), 2},
		{"definite_then_context", append(pgpRFCLiteral("PGP confidential ", "", 't'), pgpRFCLiteral("sentinel", "context", 't')...), 2},
		{"mixed_sequence", append(append(pgpRFCLiteral("", "", 't'), partial...), pgpRFCLiteral("sentinel", "context", 'u')...), 3},
		{"individual_metadata", append(
			pgpRFCFrame(11, append([]byte{'t', 1, 'a', 1, 2, 3, 4}, []byte("PGP confidential ")...), ""),
			pgpRFCFrame(11, append([]byte{'u', 2, 'b', 'c', 5, 6, 7, 8}, []byte("sentinel")...), "")...,
		), 2},
	} {
		for _, corrupt := range []string{"", "mdc"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/bad_mdc_%t/prepared_%t", tc.name, corrupt != "", prepared), func(t *testing.T) {
					packet := pgpRFCBuild(password, tc.inner, "", corrupt)
					d, err := pgpRFCDecode(packet, password)
					if corrupt == "" {
						if err != nil || d.text != "PGP confidential sentinel" || d.literalPackets != tc.literals {
							t.Fatal("independent authenticated grammar fixture", err)
						}
					} else if err == nil || d.text != "" {
						t.Fatal("independent decoder published damaged-MDC text")
					}
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, _ bool) {
						if corrupt == "" {
							pgpRemedyText(t, p, c, prepared, packet, password, "PGP confidential sentinel")
						} else {
							pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, password)
						}
					})
				})
			}
		}
	}
	for _, malformed := range []struct {
		name string
		body []byte
	}{
		{"later_filename", []byte{'t', 255, 0, 0, 0, 0}},
		{"later_timestamp", []byte{'t', 0, 0}},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			inner := append(pgpRFCLiteral("PGP confidential sentinel", "", 't'), pgpRFCFrame(11, malformed.body, "")...)
			packet := pgpRFCBuild(password, inner, "", "")
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, _ bool) {
				for _, prepared := range []bool{false, true} {
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, password)
				}
			})
		})
	}
	// A context-sized SKESK is still forbidden. The independent reader's
	// policy differs solely by the caller's outer/authenticated-inner region.
	t.Run("outer_context_rejected", func(t *testing.T) {
		valid := pgpRFCBuild("foobar", pgpRFCLiteral("text", "", 't'), "", "")
		key, data, err := pgpRFCRead(valid, false)
		if err != nil {
			t.Fatal(err)
		}
		packet := append(pgpRFCFrame(3, key.body, "context"), data...)
		if _, _, err := pgpRFCRead(packet, false); err == nil {
			t.Fatal("outer context grammar accepted")
		}
		inner, tail, err := pgpRFCRead(pgpRFCLiteral("text", "context", 't'), true)
		if err != nil || inner.tag != 11 || len(inner.body) != 10 || len(tail) != 0 {
			t.Fatal("bounded inner context grammar rejected", err)
		}
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, _ bool) {
			for _, prepared := range []bool{false, true} {
				pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "foobar")
			}
		})
	})
}

// parse_literal_data flags every format other than t/u, after structural
// validation. pgp_decrypt reports Not text data only after complete MDC proof.
func TestReferencePgcryptoPGPLiteralFormats(t *testing.T) {
	pgpRFCAnchor(t)
	for _, tc := range []struct {
		name    string
		inner   []byte
		message string
	}{
		{"text", pgpRFCLiteral("PGP confidential sentinel", "", 't'), ""},
		{"unicode", pgpRFCLiteral("PGP confidential sentinel", "", 'u'), ""},
		{"binary", pgpRFCLiteral("PGP confidential sentinel", "", 'b'), "Not text data"},
		{"nonstandard", pgpRFCLiteral("PGP confidential sentinel", "", 'x'), "Not text data"},
		{"zero", pgpRFCLiteral("PGP confidential sentinel", "", 0), "Not text data"},
		{"high", pgpRFCLiteral("PGP confidential sentinel", "", 255), "Not text data"},
		{"text_then_nontext", append(pgpRFCLiteral("PGP confidential ", "", 't'), pgpRFCLiteral("sentinel", "", 'x')...), "Not text data"},
		{"nontext_then_text", append(pgpRFCLiteral("PGP confidential ", "", 'x'), pgpRFCLiteral("sentinel", "", 't')...), "Not text data"},
		{"nontext_then_truncated", append(pgpRFCLiteral("PGP confidential sentinel", "", 'x'), pgpRFCFrame(11, []byte{'t', 0, 0}, "")...), "Wrong key or corrupt data"},
		{"nontext_bad_filename", pgpRFCFrame(11, []byte{'x', 255, 0, 0, 0, 0}, ""), "Wrong key or corrupt data"},
		{"nontext_bad_timestamp", pgpRFCFrame(11, []byte{'x', 0, 0}, ""), "Wrong key or corrupt data"},
	} {
		for _, corrupt := range []string{"", "mdc"} {
			t.Run(fmt.Sprintf("%s/bad_mdc_%t", tc.name, corrupt != ""), func(t *testing.T) {
				packet := pgpRFCBuild("pgp-private-password", tc.inner, "", corrupt)
				message := tc.message
				if corrupt != "" {
					message = "Wrong key or corrupt data"
				}
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, _ bool) {
					for _, prepared := range []bool{false, true} {
						if message == "" {
							pgpRemedyText(t, p, c, prepared, packet, "pgp-private-password", "PGP confidential sentinel")
						} else {
							pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", message, packet, "pgp-private-password")
						}
						// The same borrower remains usable after each rejected producer.
						pgpRemedyText(t, p, c, prepared, pgpVector(t), "foobar", "Secret message.")
					}
				})
			})
		}
	}
}

// REL_18_6 parse_old_len/parse_new_len cap each chunk at 16 MiB, not a
// packet's aggregate. RFC partial sizes are powers of two: the first partial
// size above 16 MiB is 32 MiB; a definite terminal can be exactly 16 MiB+1.
func TestReferencePgcryptoPGPChunkLimits(t *testing.T) {
	pgpRFCAnchor(t)
	const limit = 16 * 1024 * 1024
	for _, tc := range []struct {
		name                 string
		textBytes            int
		innerForm, outerForm string
		outerBytes           int
		reject               bool
	}{
		// SEIPD body = version1 + prefix18 + literal header6/body6/text + MDC22.
		{"outer_new_exact", limit - 53, "five", "five", limit, false},
		{"outer_new_above", limit + 1 - 53, "five", "five", limit + 1, true},
		{"inner_old_exact", limit - 6, "old4", "partial", 0, false},
		{"inner_old_above", limit + 1 - 6, "old4", "partial", 0, true},
		{"inner_new_exact", limit - 6, "five", "partial", 0, false},
		{"inner_new_above", limit + 1 - 6, "five", "partial", 0, true},
		{"outer_partial_exact", limit - 53, "five", "partial24", limit, false},
		{"outer_partial_large_aggregate", limit + 1 - 53, "five", "partial24", limit + 1, false},
		{"inner_partial_exact", limit - 6, "partial24", "partial", 0, false},
		{"inner_partial_large_aggregate", limit + 1 - 6, "partial24", "partial", 0, false},
		{"inner_terminal_exact", limit + 16384 - 6, "partial-terminal", "partial", 0, false},
		{"inner_terminal_above", limit + 1 + 16384 - 6, "partial-terminal", "partial", 0, true},
		{"small_partial_large_aggregate", limit + 1, "partial", "partial", 0, false},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", tc.name, prepared), func(t *testing.T) {
				// A fresh scenario per large case/mode preserves the fixture budget.
				// Every declared byte is supplied, unlike short oversized declarations.
				text := strings.Repeat("z", tc.textBytes)
				inner := pgpRFCLiteral(text, tc.innerForm, 't')
				literal, tail, err := pgpRFCRead(inner, true)
				if err != nil || len(tail) != 0 || len(literal.body) != tc.textBytes+6 {
					t.Fatal("actual supplied literal body length", err)
				}
				pgpRemedyLength(t, inner, tc.innerForm, tc.textBytes+6)
				packet := pgpRFCBuild("foobar", inner, tc.outerForm, "")
				_, dataWire, err := pgpRFCRead(packet, false)
				if err != nil {
					t.Fatal(err)
				}
				data, tail, err := pgpRFCRead(dataWire, false)
				if err != nil || len(tail) != 0 || (tc.outerBytes != 0 && len(data.body) != tc.outerBytes) {
					t.Fatal("actual supplied encrypted body length", err)
				}
				pgpRemedyLength(t, dataWire, tc.outerForm, len(data.body))
				// Release-invalid chunks remain RFC-valid and fully authenticated.
				// Thus rejection cannot be explained by truncation or a damaged MDC.
				d, err := pgpRFCDecode(packet, "foobar")
				if err != nil || d.text != text || d.literalPackets != 1 {
					t.Fatal("independent complete large fixture authentication", err)
				}
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, _ bool) {
					if tc.reject {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "foobar")
					} else {
						pgpRemedyText(t, p, c, prepared, packet, "foobar", text)
					}
				})
			})
		}
	}
}
