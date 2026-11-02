package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPNullAndMetadata(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, fn := range []struct {
			name, typ string
			args      []any
			firstType string
		}{
			{"pgp_sym_encrypt", "BYTEA", []any{"text", "foobar"}, "text"},
			{"pgp_sym_encrypt", "BYTEA", []any{"text", "foobar", ""}, "text"},
			{"pgp_sym_decrypt", "TEXT", []any{pgpVector(t), "foobar"}, "bytea"},
			{"pgp_sym_decrypt", "TEXT", []any{pgpVector(t), "foobar", ""}, "bytea"},
		} {
			q := "SELECT " + fn.name + "($1::" + fn.firstType + ",$2::text"
			if len(fn.args) == 3 {
				q += ",$3::text"
			}
			q += ")"
			for i := range fn.args {
				args := append([]any(nil), fn.args...)
				args[i] = nil
				// STRICT applies before option parsing even when another non-NULL
				// argument supplies a malformed option value.
				if len(args) == 3 && i != 2 {
					args[2] = "unknown=1"
				}
				s := fixture.Select(q, []string{fn.name}, []string{fn.typ}, [][]any{{nil}}, args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgpMetadata(t, rows, fn.name, fn.typ, false, false) }
				pgpBoth(t, p, c, r, s)
			}
		}
		for _, tc := range []struct{ q, name, typ string }{
			{"SELECT pgp_sym_encrypt(NULL::text,'foobar')", "pgp_sym_encrypt", "BYTEA"},
			{"SELECT pgp_sym_encrypt('text',NULL::text)", "pgp_sym_encrypt", "BYTEA"},
			{"SELECT pgp_sym_encrypt('text','foobar',NULL::text)", "pgp_sym_encrypt", "BYTEA"},
			{"SELECT pgp_sym_decrypt(NULL::bytea,'foobar')", "pgp_sym_decrypt", "TEXT"},
			{"SELECT pgp_sym_decrypt('bad'::bytea,NULL::text,'unknown=1')", "pgp_sym_decrypt", "TEXT"},
			{"SELECT pgp_sym_decrypt('bad'::bytea,'foobar',NULL::text)", "pgp_sym_decrypt", "TEXT"},
		} {
			pgpBoth(t, p, c, r, fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{nil}}))
		}
		// A NULL earlier child cannot swallow an error from a demanded later
		// parameter expression. No constant-folding dependence in this witness.
		for _, q := range []string{
			"SELECT pgp_sym_encrypt($1::text,($2::integer/$3::integer)::text)",
			"SELECT pgp_sym_decrypt($1::bytea,($2::integer/$3::integer)::text)",
		} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, nil, 1, 0)
			s.SelectRows = true
			pgpBoth(t, p, c, r, s)
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q, name string
				args    []any
			}{
				{"SELECT pgp_sym_decrypt($1::bytea,$2::text)", "pgp_sym_decrypt", []any{pgpVector(t), "foobar"}},
				{"SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text) AS decoded", "decoded", []any{pgpVector(t), "foobar", ""}},
			} {
				rows := pgpQuery(t, p, c, prepared, tc.q, tc.args...)
				metadata := pgpMetadata(t, rows, tc.name, "TEXT", false, false)
				if !rows.Next() {
					t.Fatal("missing decrypt row", rows.Err())
				}
				var text string
				if err := rows.Scan(&text); err != nil || text != "Secret message." {
					t.Fatal("decrypt Scan result", err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("decrypt row tail", rows.Err())
				}
				pgpColumns(t, metadata, tc.name, "TEXT", false, false)
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				pgpColumns(t, metadata, tc.name, "TEXT", false, false)
			}
		}
	})
}
