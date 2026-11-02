package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgpBoth(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func pgpDiagnostic(message string) fixture.Diagnostic {
	// Independently specified diagnostics confirmed by root's pinned
	// PostgreSQL 18.6 checkpoint, including absent ancillary logical fields.
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: message}
}

func pgpTextStep(q, text string, args ...any) fixture.Step {
	return fixture.Select(q, []string{"pgp_sym_decrypt"}, []string{"TEXT"}, [][]any{{text}}, args...)
}

func pgpMetadata(t *testing.T, rows *sql.Rows, name, typ string, nullKnown, nullable bool) []*sql.ColumnType {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgpColumns(t, cols, name, typ, nullKnown, nullable)
	return cols
}

func pgpColumns(t *testing.T, cols []*sql.ColumnType, name, typ string, nullKnown, nullable bool) {
	t.Helper()
	wantScan := reflect.TypeOf("")
	if typ == "BYTEA" {
		wantScan = reflect.TypeOf([]byte{})
	}
	if len(cols) != 1 || cols[0].Name() != name || cols[0].DatabaseTypeName() != typ || cols[0].ScanType() != wantScan {
		t.Fatal("PGP result descriptors", cols)
	}
	if n, known := cols[0].Nullable(); n != nullable || known != nullKnown {
		t.Fatal("PGP nullability", n, known, nullable, nullKnown)
	}
}

func pgpQuery(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, q string, args ...any) *sql.Rows {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { stmt.Close() })
		rows, err = stmt.QueryContext(p.Ctx, args...)
	} else {
		rows, err = c.QueryContext(p.Ctx, q, args...)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	return rows
}

func pgpEncryptCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, text, password string, options *string, count int) []byte {
	t.Helper()
	q := "SELECT pgp_sym_encrypt($1::text,$2::text)"
	args := []any{text, password}
	if options != nil {
		q = "SELECT pgp_sym_encrypt($1::text,$2::text,$3::text)"
		args = append(args, *options)
	}
	rows := pgpQuery(t, p, c, prepared, q, args...)
	return pgpCipherRows(t, rows, text, password, count)
}

func pgpCipherRows(t *testing.T, rows *sql.Rows, text, password string, count int) []byte {
	t.Helper()
	defer rows.Close()
	metadata := pgpMetadata(t, rows, "pgp_sym_encrypt", "BYTEA", false, false)
	if !rows.Next() {
		t.Fatal("missing encrypted row", rows.Err())
	}
	var packet []byte
	if err := rows.Scan(&packet); err != nil {
		t.Fatal(err)
	}
	d, err := pgpRFCDecode(packet, password)
	if err != nil {
		t.Fatal("independent emitted packet decode", err)
	}
	if d.text != text {
		t.Fatal("emitted literal differs from input")
	}
	if d.literalPackets != 1 || d.literalFormat != 't' || d.filenameLength != 0 {
		t.Fatal("emitted literal profile", d.literalFormat, d.filenameLength)
	}
	if count != 0 && d.count != count {
		t.Fatal("encoded S2K count", d.count, count)
	}
	if count == 0 && (d.count < 65536 || d.count > 253952) {
		t.Fatal("default randomized count range", d.count)
	}
	if len(text) >= 32768 && !d.outerPartial {
		t.Fatal("long output lacks partial framing")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("encrypted row tail", rows.Err())
	}
	// database/sql closes Rows at EOF on both adapters; retain the genuine
	// descriptors obtained while open rather than querying a closed cursor.
	pgpColumns(t, metadata, "pgp_sym_encrypt", "BYTEA", false, false)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	pgpColumns(t, metadata, "pgp_sym_encrypt", "BYTEA", false, false)
	return packet
}

func pgpNoLeak(t *testing.T, p *fixture.Pair, c *sql.Conn, prepared bool, q string, message string, args ...any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal("parameterized prepare", e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx, args...)
	} else {
		rows, err = c.QueryContext(p.Ctx, q, args...)
	}
	if rows != nil {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("failed packet exposed plaintext row")
		}
		if err == nil {
			err = rows.Err()
		}
		if rows.Next() {
			t.Fatal("failed packet exposed later row")
		}
	}
	d := pgpDiagnostic(message)
	fixture.AssertDiagnostic(t, err, &d)
	if err == nil || strings.Contains(err.Error(), "PGP confidential sentinel") || strings.Contains(err.Error(), "pgp-private-password") {
		t.Fatal("PGP error leaked input or was absent")
	}
}
