package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func symByteStep(q string, value []byte, args ...any) fixture.Step {
	return fixture.Select(q, []string{"pgp_sym_decrypt_bytea"}, []string{"BYTEA"}, [][]any{{string(value)}}, args...)
}

func symDecrypt(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, packet []byte, password string, want []byte, text bool, options *string) {
	t.Helper()
	name, typ := "pgp_sym_decrypt_bytea", "BYTEA"
	if text {
		name, typ = "pgp_sym_decrypt", "TEXT"
	}
	q := "SELECT " + name + "($1::bytea,$2::text"
	args := []any{packet, password}
	if options != nil {
		q += ",$3::text"
		args = append(args, *options)
	}
	q += ")"
	s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{string(want)}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgpMetadata(t, rows, name, typ, false, false) }
	pgpBoth(t, p, c, r, s)
}

// Inspect actual randomized backend emissions, not encryption self-roundtrip or
// ciphertext equality. The independent decoder checks prefix, SHA1 MDC, session
// framing and complete literal grammar before these exact profile assertions.
func symEncrypt(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared, binary bool, input []byte, password string, options *string, want symProfile, format byte, wire []byte, count int) []byte {
	t.Helper()
	name, first := "pgp_sym_encrypt", "text"
	var value any = string(input)
	if binary {
		name, first, value = "pgp_sym_encrypt_bytea", "bytea", input
	}
	q := "SELECT " + name + "($1::" + first + ",$2::text"
	args := []any{value, password}
	if options != nil {
		q += ",$3::text"
		args = append(args, *options)
	}
	q += ")"
	rows := pgpQuery(t, p, c, prepared, q, args...)
	return symCipherRows(t, rows, name, password, want, format, wire, count)
}

func symCipherRows(t *testing.T, rows *sql.Rows, name, password string, want symProfile, format byte, wire []byte, count int) []byte {
	t.Helper()
	defer rows.Close()
	metadata := pgpMetadata(t, rows, name, "BYTEA", false, false)
	if !rows.Next() {
		t.Fatal("missing symmetric ciphertext", rows.Err())
	}
	var packet []byte
	if err := rows.Scan(&packet); err != nil {
		t.Fatal(err)
	}
	d, err := symDecode(packet, password)
	if err != nil {
		t.Fatal("independent symmetric emission decode", err)
	}
	got := d.profile
	// Default randomized iteration counts are checked separately, not pinned.
	got.count, want.count = 0, 0
	if got != want || d.keySize != symKeySize(want.cipher) || d.filenameLength != 0 || !bytes.Equal(d.formats, []byte{format}) || !bytes.Equal(d.data, wire) {
		t.Fatalf("emitted symmetric semantics: profile=%+v want=%+v key=%d formats=%v filename=%d payloadEqual=%v", got, want, d.keySize, d.formats, d.filenameLength, bytes.Equal(d.data, wire))
	}
	salt := 8
	if want.mode == 0 {
		salt = 0
	}
	if d.saltSize != salt {
		t.Fatal("S2K salt size", d.saltSize, salt)
	}
	if want.mode == 3 {
		if count == 0 {
			if d.count < 65536 || d.count > 253952 {
				t.Fatal("default S2K count", d.count)
			}
		} else if d.count != count {
			t.Fatal("rounded S2K count", d.count, count)
		}
	} else if d.count != 0 {
		t.Fatal("uniterated S2K has count")
	}
	if len(wire) >= 32768 && want.compression == 0 && !d.partial {
		t.Fatal("long ciphertext lacks partial framing")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("symmetric ciphertext tail", rows.Err())
	}
	pgpColumns(t, metadata, name, "BYTEA", false, false)
	if _, err := rows.ColumnTypes(); err == nil || err.Error() != "sql: Rows are closed" {
		t.Fatal("closed ColumnTypes", err)
	}
	if _, err := rows.Columns(); err == nil || err.Error() != "sql: Rows are closed" {
		t.Fatal("closed Columns", err)
	}
	return packet
}

func symError(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, q string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal("symmetric parameter prepare", e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx, args...)
	} else {
		rows, err = c.QueryContext(p.Ctx, q, args...)
	}
	if rows != nil {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("failed symmetric call published row")
		}
		if err == nil {
			err = rows.Err()
		}
		if rows.Next() {
			t.Fatal("failed symmetric call published later row")
		}
	}
	fixture.AssertDiagnostic(t, err, &want)
}

func symRouteName(prepared bool) string {
	if prepared {
		return "prepared"
	}
	return "direct"
}

func symOptions(p symProfile) string {
	cipherNames := map[byte]string{2: "3des", 3: "cast5", 4: "bf", 7: "aes128", 8: "aes192", 9: "aes256"}
	digests := map[byte]string{1: "md5", 2: "sha1", 8: "sha256"}
	s := fmt.Sprintf("cipher-algo=%s,s2k-cipher-algo=%s,s2k-digest-algo=%s,s2k-mode=%d", cipherNames[p.cipher], cipherNames[p.wrap], digests[p.digest], p.mode)
	if p.mode == 3 {
		s += fmt.Sprintf(",s2k-count=%d", pgpRFCCount(p.count))
	}
	if p.session {
		s += ",sess-key=1"
	}
	if p.noMDC {
		s += ",disable-mdc=1"
	}
	return s + fmt.Sprintf(",compress-algo=%d", p.compression)
}
