package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoDiagnosticsAndNull(t *testing.T) {
	for _, name := range []string{"NO_SUCH_HASH", "", " SHA256", "SHA256 ", strings.Repeat("A", 70), strings.Repeat("A", 62) + "éZ", "ÄSHA256"} {
		t.Run(fmt.Sprintf("algorithm_%q", name), func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				normalized := strings.Map(func(ch rune) rune {
					if ch >= 'A' && ch <= 'Z' {
						return ch + 32
					}
					return ch
				}, name)
				if len(normalized) > 63 {
					normalized = normalized[:63]
					if strings.HasSuffix(normalized, "\xc3") {
						normalized = normalized[:62]
					}
				}
				for _, function := range []string{"digest", "hmac"} {
					prefix := "'abc',"
					parameter := "$1,$2"
					args := []any{"abc", name}
					if function == "hmac" {
						prefix = "'abc','key',"
						parameter = "$1,$2,$3"
						args = []any{"abc", "key", name}
					}
					literal := "SELECT " + function + "(" + prefix + "'" + name + "')"
					bound := "SELECT " + function + "(" + parameter + ")"
					for _, s := range []fixture.Step{pgcryptoUnsupported(literal, normalized), pgcryptoUnsupported(bound, normalized, args...)} {
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, r, s)
						fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
					}
					stmt, err := c.PrepareContext(p.Ctx, bound)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: bound}, r, pgcryptoUnsupported(bound, normalized, args...))
					stmt.Close()
				}
				var notices []fixture.Diagnostic
				_, noticeConn, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
				fixture.RunStep(t, p.Ctx, noticeConn, nr, pgcryptoUnsupported("SELECT digest('abc',$1)", normalized, name))
				if len(notices) != 0 {
					t.Fatalf("algorithm normalization emitted notices: %#v", notices)
				}
			})
		})
	}
	t.Run("strict_null_and_demand", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			run := func(s fixture.Step) {
				t.Helper()
				t.Run(fmt.Sprintf("prepared=%v SQL=%s args=%#v", s.Prepared, s.Query, s.Args), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, s)
				})
			}
			for _, q := range []string{"SELECT digest(NULL,'NO_SUCH_HASH')", "SELECT digest('abc',NULL)", "SELECT digest(NULL::bytea,'NO_SUCH_HASH')", "SELECT hmac(NULL,'key','NO_SUCH_HASH')", "SELECT hmac('abc',NULL,'NO_SUCH_HASH')", "SELECT hmac('abc','key',NULL)", "SELECT hmac(NULL::bytea,'key'::bytea,'NO_SUCH_HASH')"} {
				name := "digest"
				if strings.Contains(q, "hmac") {
					name = "hmac"
				}
				s := fixture.Select(q, []string{name}, []string{"BYTEA"}, [][]any{{nil}})
				run(s)
				run(fixture.Prepared(s))
			}
			for _, cast := range []string{"text", "bytea"} {
				for _, name := range []string{"digest", "hmac"} {
					q := "SELECT digest($1::" + cast + ",$2)"
					frames := [][]any{{nil, "NO_SUCH_HASH"}, {"abc", nil}}
					if name == "hmac" {
						q = "SELECT hmac($1::" + cast + ",$2::" + cast + ",$3)"
						frames = [][]any{{nil, "key", "NO_SUCH_HASH"}, {"abc", nil, "NO_SUCH_HASH"}, {"abc", "key", nil}}
					}
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, args := range frames {
						s := fixture.Select(q, []string{name}, []string{"BYTEA"}, [][]any{{nil}}, args...)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, s)
					}
					stmt.Close()
				}
			}
			run(fixture.Exec("CREATE SEQUENCE pgcrypto_effect", 0))
			run(fixture.Exec("CREATE TABLE pgcrypto_null(v text)", 0))
			run(fixture.Exec("INSERT INTO pgcrypto_null VALUES(NULL)", 1))
			run(fixture.Select("SELECT hmac(v,nextval('pgcrypto_effect')::text,'sha256') FROM pgcrypto_null", []string{"hmac"}, []string{"BYTEA"}, [][]any{{nil}}))
			run(fixture.Select("SELECT currval('pgcrypto_effect')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.ImplicitError("SELECT digest(v,(1/0)::text) FROM pgcrypto_null", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			run(fixture.Select("SELECT CASE WHEN false THEN digest('abc','NO_SUCH_HASH') ELSE NULL::bytea END", []string{"case"}, []string{"BYTEA"}, [][]any{{nil}}))
			sibling := fixture.ImplicitError("SELECT digest($1::text,($2::integer/$3::integer)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, nil, 1, 0)
			run(sibling)
			run(fixture.Prepared(sibling))
			// Pinned PostgreSQL evaluates this constant sibling despite literal NULL.
			literal := fixture.ImplicitError("SELECT digest(NULL,(1/0)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			literal.ErrorStage = "execute"
			run(literal)
			run(fixture.Prepared(literal))
		})
	})
	// Alias acceptance is a checkpoint question, isolated from canonical vectors.
	for _, alias := range []string{"sha-1", "sha-224", "sha-256", "sha-384", "sha-512", "SHA2-256", "MD5", "SHA256"} {
		t.Run("alias_"+alias, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				canonical := strings.ReplaceAll(strings.ToLower(alias), "-", "")
				if canonical == "sha2256" {
					canonical = "sha256"
				}
				for _, vector := range pgcryptoDigestVectors {
					if vector.algorithm == canonical {
						pgcryptoResult(t, p, c, r, "digest", "SELECT digest('abc',$1)", vector.abc, alias)
					}
				}
			})
		})
	}
}
