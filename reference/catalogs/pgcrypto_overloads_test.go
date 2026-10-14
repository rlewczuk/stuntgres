package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoOverloadsAndParameters(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("pgcrypto step prepared=%v SQL=%s args=%#v", s.Prepared, s.Query, s.Args)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		sha := pgcryptoDigestVectors[3].abc
		for _, q := range []string{"SELECT digest($1,$2)", "SELECT digest($1::varchar,$2)", "SELECT digest($1::bytea,$2)", "SELECT DIGEST($1,$2)"} {
			pgcryptoResult(t, p, c, r, "digest", q, sha, "abc", "sha256")
		}
		// Unknown parameters infer TEXT; the pinned pq fixture supplies abc octets
		// for this []byte bind, not the ASCII bytea spelling \x616263.
		inferred := fixture.Select("SELECT digest($1,$2)", []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, sha)}}, []byte("abc"), "sha256")
		run(inferred)
		run(fixture.Prepared(inferred))
		run(fixture.Exec("CREATE DOMAIN pgcrypto_text AS text", 0))
		run(fixture.Exec("CREATE DOMAIN pgcrypto_binary AS bytea", 0))
		for _, q := range []string{"SELECT digest('abc'::pgcrypto_text,'sha256')", "SELECT digest('abc'::pgcrypto_binary,'sha256')"} {
			pgcryptoResult(t, p, c, r, "digest", q, sha)
		}
		jefe := "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
		for _, q := range []string{"SELECT hmac($1,$2,$3)", "SELECT hmac($1::bytea,$2,$3)", "SELECT hmac($1,$2::bytea,$3)"} {
			pgcryptoResult(t, p, c, r, "hmac", q, jefe, "what do ya want for nothing?", "Jefe", "sha256")
		}
		for _, tc := range []struct{ q, name, args string }{
			{"SELECT hmac('a'::text,'b'::bytea,'sha256')", "hmac", "text, bytea, unknown"},
			{"SELECT hmac('a'::bytea,'b'::text,'sha256')", "hmac", "bytea, text, unknown"},
			{"SELECT digest(1,'sha256')", "digest", "integer, unknown"},
			{"SELECT digest('abc')", "digest", "unknown"},
			{"SELECT hmac('abc','key')", "hmac", "unknown, unknown"},
			{"SELECT \"Digest\"('abc','sha256')", "Digest", "unknown, unknown"},
		} {
			t.Run(tc.args+tc.name, func(t *testing.T) {
				s := extensionMissing(tc.q, tc.name, tc.args)
				fixture.RunStep(t, p.Ctx, c, r, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, r, s)
			})
		}
		bad := fixture.ImplicitError(`SELECT digest('\x0g'::bytea,'sha256')`, fixture.Diagnostic{Code: "22023", Message: "invalid hexadecimal digit: \"g\"", Position: "15"})
		run(bad)
		bad = fixture.Prepared(bad)
		bad.ErrorStage = "prepare"
		run(bad)
		// A binary bind is octets, not contextual hexadecimal input.
		pgcryptoResult(t, p, c, r, "digest", "SELECT digest($1::bytea,$2)", sha, []byte("abc"), "sha256")
		// Metadata survives zero rows, NULL-only rows, EOF and Close.
		run(fixture.Exec("CREATE TABLE pgcrypto_inputs(v text)", 0))
		for _, q := range []string{"SELECT digest(v,'sha256') FROM pgcrypto_inputs", "SELECT digest(NULL,'sha256')"} {
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(columns) != 1 || columns[0].Name() != "digest" || columns[0].DatabaseTypeName() != "BYTEA" || columns[0].ScanType() != reflect.TypeOf([]byte{}) {
				t.Fatalf("BYTEA metadata %#v", columns)
			}
			length, lengthKnown := columns[0].Length()
			if pg {
				if !lengthKnown || length != 9223372036854775807 {
					t.Fatalf("pq BYTEA length %d %v", length, lengthKnown)
				}
			} else if lengthKnown {
				t.Fatalf("native BYTEA length unexpectedly known: %d", length)
			}
			nullable, ok := columns[0].Nullable()
			if nullable || ok {
				t.Fatal("computed nullable metadata", nullable, ok)
			}
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil || got != nil {
					t.Fatal(got, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if columns[0].DatabaseTypeName() != "BYTEA" {
				t.Fatal("retained metadata changed")
			}
		}
		// Native precancel guarantees are not a stable fast-command pq contract.
		// See docs/TESTING.md: cancellation must not invent pq transport health.
		if !pg {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if rows, err := c.QueryContext(ctx, "SELECT digest('abc','sha256')"); !errors.Is(err, context.Canceled) || rows != nil {
				t.Fatal("precancel Query", rows, err)
			}
			if stmt, err := c.PrepareContext(ctx, "SELECT hmac($1,$2,$3)"); !errors.Is(err, context.Canceled) || stmt != nil {
				t.Fatal("precancel Prepare", stmt, err)
			}
		}
	})
}
