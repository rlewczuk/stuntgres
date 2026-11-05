package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPSymmetricDiagnostics(t *testing.T) {
	symAnchor(t)
	password := "pgp-private-password"
	profile := symProfile{cipher: 9, wrap: 3, digest: 8, mode: 3, session: true}
	inner := pgpRFCLiteral("PGP confidential sentinel", "", 't')
	valid := symBuild(password, inner, profile, "")
	key, rest, err := pgpRFCRead(valid, false)
	if err != nil {
		t.Fatal(err)
	}
	mutateKey := func(offset int, value byte) []byte {
		body := append([]byte(nil), key.body...)
		body[offset] = value
		return append(pgpRFCFrame(3, body, ""), rest...)
	}
	data, _, err := pgpRFCRead(rest, false)
	if err != nil {
		t.Fatal(err)
	}
	badVersion := append([]byte(nil), data.body...)
	badVersion[0] = 2
	flip := append([]byte(nil), valid...)
	flip[len(flip)-1] ^= 1
	for _, tc := range []struct {
		name          string
		packet        []byte
		pass, message string
	}{
		{"wrong_password", valid, "incorrect", "Wrong key or corrupt data"},
		{"tamper", flip, password, "Wrong key or corrupt data"},
		{"truncated", valid[:len(valid)-1], password, "Wrong key or corrupt data"},
		{"prefix", symBuild(password, inner, profile, "prefix"), password, "Wrong key or corrupt data"},
		{"mdc", symBuild(password, inner, profile, "mdc"), password, "Wrong key or corrupt data"},
		{"session_algorithm", symBuild(password, inner, profile, "session-algorithm"), password, "Wrong key or corrupt data"},
		{"session_length", symBuild(password, inner, profile, "session-length"), password, "Wrong key or corrupt data"},
		{"s2k_mode", mutateKey(2, 2), password, "Bad S2K mode"},
		{"s2k_digest", mutateKey(3, 127), password, "Wrong key or corrupt data"},
		{"s2k_cipher", mutateKey(1, 127), password, "Unsupported cipher algorithm"},
		{"skesk_version", mutateKey(0, 5), password, "Wrong key or corrupt data"},
		{"data_version", append(pgpRFCFrame(3, key.body, ""), pgpRFCFrame(18, badVersion, "")...), password, "Wrong key or corrupt data"},
		{"duplicate_key", append(pgpRFCFrame(3, key.body, ""), valid...), password, "Wrong key or corrupt data"},
		{"missing_key", rest, password, "Wrong key or corrupt data"},
		{"key_only", pgpRFCFrame(3, key.body, ""), password, "Wrong key or corrupt data"},
		{"suffix", append(append([]byte(nil), valid...), 0), password, "Wrong key or corrupt data"},
		{"empty", []byte{}, password, "Wrong key or corrupt data"},
		{"late_malformed_after_binary", symBuild(password, append(pgpRFCLiteral("binary", "", 'b'), pgpRFCFrame(11, []byte{'t', 255}, "")...), profile, ""), password, "Wrong key or corrupt data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				for _, name := range []string{"pgp_sym_decrypt", "pgp_sym_decrypt_bytea"} {
					for _, prepared := range []bool{false, true} {
						pgpNoLeak(t, p, c, prepared, "SELECT "+name+"($1::bytea,$2::text)", tc.message, tc.packet, tc.pass)
					}
				}
			})
		})
	}
	for _, tc := range []struct{ name, options, message string }{
		{"unknown", "unknown=1", "Illegal argument to function"},
		{"missing_equals", "cipher-algo aes256", "Illegal argument to function"},
		{"missing_value", "cipher-algo=", "Illegal argument to function"},
		{"whitespace", " \t\n", "Illegal argument to function"},
		{"cr_key", "\rcipher-algo=aes256", "Illegal argument to function"},
		{"cr_value", "cipher-algo=aes256\r", "Unsupported cipher algorithm"},
		{"count_low", "s2k-count=1023", "Illegal argument to function"},
		{"count_high", "s2k-count=65011713", "Illegal argument to function"},
		{"count_max_int", "s2k-count=2147483647", "Illegal argument to function"},
		{"count_min_int", "s2k-count=-2147483648", "Illegal argument to function"},
		{"mode", "s2k-mode=2", "Illegal argument to function"},
		{"count_encounter", "s2k-mode=1,s2k-count=1024,s2k-mode=3", "Illegal argument to function"},
		{"bad_duplicate", "s2k-count=1023,s2k-count=1024", "Illegal argument to function"},
		{"cipher_duplicate", "cipher-algo=nope,cipher-algo=aes256", "Unsupported cipher algorithm"},
		{"wrap", "s2k-cipher-algo=nope", "Unsupported cipher algorithm"},
		{"digest", "s2k-digest-algo=nope", "Unsupported digest algorithm"},
		{"compress", "compress-algo=4", "Illegal argument to function"},
		{"level_high", "compress-level=10", "Illegal argument to function"},
		{"level_low", "compress-level=-1", "Illegal argument to function"},
		{"double_comma", "s2k-count=1024,,sess-key=1", "Illegal argument to function"},
	} {
		t.Run("options/"+tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				for _, fn := range []struct {
					name, first string
					input       any
				}{
					{"pgp_sym_encrypt", "text", "PGP confidential sentinel"},
					{"pgp_sym_encrypt_bytea", "bytea", []byte("PGP confidential sentinel")},
					{"pgp_sym_decrypt", "bytea", valid}, {"pgp_sym_decrypt_bytea", "bytea", valid},
				} {
					q := "SELECT " + fn.name + "($1::" + fn.first + ",$2::text,$3::text)"
					for _, prepared := range []bool{false, true} {
						pgpNoLeak(t, p, c, prepared, q, tc.message, fn.input, password, tc.options)
						if strings.Contains(fn.name, "decrypt") {
							pgpNoLeak(t, p, c, prepared, q, tc.message, []byte{0}, password, tc.options)
						}
					}
				}
			})
		})
	}
	t.Run("body_controls_and_recovery", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			brokenBF := symBase64(t, "ww0EBAMC8wIKbtvzJtxi0jABUleCwFJWGCkYKcsNdABqdtXaU2VjcmV0LtMUlnPH3A2QBmZrcucm1GPb/s2Bkdg=")
			for _, prepared := range []bool{false, true} {
				for _, options := range []string{"ignore-cipher-failure=0", "ignore-cipher-failure=1"} {
					pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "Wrong key or corrupt data", brokenBF, "wrong key", options)
				}
				for _, fn := range []struct {
					name, first string
					input       any
				}{
					{"pgp_sym_encrypt", "text", "data"}, {"pgp_sym_encrypt_bytea", "bytea", []byte("data")},
					{"pgp_sym_decrypt", "bytea", valid}, {"pgp_sym_decrypt_bytea", "bytea", valid},
				} {
					pgpNoLeak(t, p, c, prepared, "SELECT "+fn.name+"($1::"+fn.first+",$2::text)", "Illegal argument to function", fn.input, "")
				}
				for _, name := range []string{"pgp_sym_encrypt", "pgp_sym_encrypt_bytea"} {
					first := "text"
					var input any = "data"
					if strings.HasSuffix(name, "bytea") {
						first = "bytea"
						input = []byte("data")
					}
					pgpNoLeak(t, p, c, prepared, "SELECT "+name+"($1::"+first+",$2::text,$3::text)", "Unsupported compression algorithm", input, password, "compress-algo=3")
				}
			}
			options := "cipher-algo=bf,s2k-cipher-algo=3des,s2k-mode=0,s2k-digest-algo=md5,compress-algo=2,disable-mdc=1,sess-key=1"
			symDecrypt(t, p, c, r, valid, password, []byte("PGP confidential sentinel"), true, &options)
			symDecrypt(t, p, c, r, valid, password, []byte("PGP confidential sentinel"), false, &options)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			pgpNoLeak(t, p, c, false, "SELECT pgp_sym_decrypt_bytea($1::bytea,$2::text)", "Wrong key or corrupt data", flip, password)
			fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
			symDecrypt(t, p, c, r, valid, password, []byte("PGP confidential sentinel"), false, nil)
		})
	})
}

func TestReferencePgcryptoPGPSymmetricNullAndMetadata(t *testing.T) {
	symAnchor(t)
	for _, fn := range []struct {
		name, first, typ string
		value            any
	}{
		{"pgp_sym_encrypt", "text", "BYTEA", "text"},
		{"pgp_sym_encrypt_bytea", "bytea", "BYTEA", []byte{0, 255}},
		{"pgp_sym_decrypt", "bytea", "TEXT", pgpVector(t)},
		{"pgp_sym_decrypt_bytea", "bytea", "BYTEA", pgpVector(t)},
	} {
		for _, arity := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s/arity_%d", fn.name, arity), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					args := []any{fn.value, "foobar"}
					if arity == 3 {
						args = append(args, "")
					}
					for _, typed := range []bool{false, true} {
						params := []string{"$1", "$2"}
						if arity == 3 {
							params = append(params, "$3")
						}
						if typed {
							params[0] += "::" + fn.first
							params[1] += "::text"
							if arity == 3 {
								params[2] += "::text"
							}
						}
						q := "SELECT " + fn.name + "(" + strings.Join(params, ",") + ")"
						for i := range args {
							nulls := append([]any(nil), args...)
							nulls[i] = nil
							if arity == 3 && i != 2 {
								nulls[2] = "unknown=1"
							}
							s := fixture.Select(q, []string{fn.name}, []string{fn.typ}, [][]any{{nil}}, nulls...)
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgpMetadata(t, rows, fn.name, fn.typ, false, false) }
							pgpBoth(t, p, c, r, s)
						}
						// Unknown parameters must infer the selected installed overload;
						// execute a non-NULL call too, not merely a typed NULL shortcut.
						if strings.Contains(fn.name, "decrypt") {
							s := fixture.Select(q, []string{fn.name}, []string{fn.typ}, [][]any{{"Secret message."}}, args...)
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgpMetadata(t, rows, fn.name, fn.typ, false, false) }
							pgpBoth(t, p, c, r, s)
						} else {
							for _, prepared := range []bool{false, true} {
								rows := pgpQuery(t, p, c, prepared, q, args...)
								pgpMetadata(t, rows, fn.name, "BYTEA", false, false)
								if !rows.Next() {
									t.Fatal("unknown-param encrypt row", rows.Err())
								}
								var packet []byte
								if err := rows.Scan(&packet); err != nil {
									t.Fatal(err)
								}
								d, err := symDecode(packet, "foobar")
								want := []byte("text")
								format := byte('t')
								if fn.first == "bytea" {
									want = []byte{0, 255}
									format = 'b'
								}
								if err != nil || !bytes.Equal(d.data, want) || !bytes.Equal(d.formats, []byte{format}) {
									t.Fatal("unknown-param encryption semantics", err)
								}
								if rows.Next() || rows.Err() != nil {
									t.Fatal("unknown-param row tail", rows.Err())
								}
								rows.Close()
							}
						}
					}
					// NULL first child does not suppress demanded password/options errors.
					for _, slot := range []int{1, 2} {
						if slot >= arity {
							continue
						}
						params := []string{"$1::" + fn.first, "($2::integer/$3::integer)::text"}
						demandArgs := []any{nil, 1, 0}
						if arity == 3 {
							params = append(params, "$4::text")
							demandArgs = append(demandArgs, "")
						}
						if slot == 2 {
							params = []string{"$1::" + fn.first, "$2::text", "($3::integer/$4::integer)::text"}
							demandArgs = []any{nil, "foobar", 1, 0}
						}
						q := "SELECT " + fn.name + "(" + strings.Join(params, ",") + ")"
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, demandArgs...)
						s.SelectRows = true
						pgpBoth(t, p, c, r, s)
					}
					// Conversely a later NULL cannot suppress an earlier child.
					firstError := "($1::integer/$2::integer)::text"
					if fn.first == "bytea" {
						firstError = "(" + firstError + ")::bytea"
					}
					q := "SELECT " + fn.name + "(" + firstError + ",$3::text"
					errorArgs := []any{1, 0, nil}
					if arity == 3 {
						q += ",$4::text"
						errorArgs = append(errorArgs, nil)
					}
					q += ")"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, errorArgs...)
					s.SelectRows = true
					pgpBoth(t, p, c, r, s)
					if arity == 3 {
						q = "SELECT " + fn.name + "($1::" + fn.first + ",($2::integer/$3::integer)::text,$4::text)"
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, fn.value, 1, 0, nil)
						s.SelectRows = true
						pgpBoth(t, p, c, r, s)
					}
				})
			})
		}
	}
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, prepared := range []bool{false, true} {
			for _, binary := range []bool{false, true} {
				name, typ := "pgp_sym_decrypt", "TEXT"
				if binary {
					name, typ = "pgp_sym_decrypt_bytea", "BYTEA"
				}
				rows := pgpQuery(t, p, c, prepared, "SELECT "+name+"($1::bytea,$2::text,$3::text) AS decoded", pgpVector(t), "foobar", "")
				cols, err := rows.Columns()
				if err != nil || len(cols) != 1 || cols[0] != "decoded" {
					t.Fatal("open Columns", cols, err)
				}
				metadata := pgpMetadata(t, rows, "decoded", typ, false, false)
				if !rows.Next() {
					t.Fatal("metadata row", rows.Err())
				}
				var snapshot []byte
				if binary {
					if err := rows.Scan(&snapshot); err != nil {
						t.Fatal(err)
					}
				} else {
					var text string
					if err := rows.Scan(&text); err != nil {
						t.Fatal(err)
					}
					snapshot = []byte(text)
				}
				if string(snapshot) != "Secret message." {
					t.Fatal("typed Scan payload")
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("metadata EOF", rows.Err())
				}
				for _, metadataCall := range []func() error{func() error { _, e := rows.Columns(); return e }, func() error { _, e := rows.ColumnTypes(); return e }} {
					if e := metadataCall(); e == nil || e.Error() != "sql: Rows are closed" {
						t.Fatal("closed metadata", e)
					}
				}
				rows.Close()
				options := "s2k-count=1024"
				symEncrypt(t, p, c, prepared, true, []byte{255, 0}, "later", &options, symDefault(), 'b', []byte{255, 0}, 1024)
				if string(snapshot) != "Secret message." || cols[0] != "decoded" {
					t.Fatal("later call mutated caller snapshots")
				}
				pgpColumns(t, metadata, "decoded", typ, false, false)
			}
		}
	})
}
