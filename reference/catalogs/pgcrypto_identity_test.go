package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Extension ownership follows the installer. Trusted installation by a
// nonsuperuser instead assigns members to bootstrap postgres; a superuser
// installer owns both. Keep the two expectations independent for replay too.
func pgcryptoInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, owner, expectedMemberOwner string) {
	t.Helper()
	// Use the supported public routine identity route, not lookup-only OIDVECTOR.
	if _, err := c.ExecContext(p.Ctx, "SET search_path TO public"); err != nil {
		t.Fatal(err)
	}
	var ext, ns int64
	var version, gotOwner string
	var relocatable bool
	if err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extnamespace,extversion,extrelocatable,rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE extname='pgcrypto'").Scan(&ext, &ns, &version, &relocatable, &gotOwner); err != nil || ext <= 0 || ns <= 0 || version != "1.4" || !relocatable || gotOwner != owner {
		t.Fatalf("extension identity %d %d %q %v %q: %v", ext, ns, version, relocatable, gotOwner, err)
	}
	pgcryptoRandomInventory(t, p, c, false, expectedMemberOwner)
}

func TestReferencePgcryptoInstalledIdentity(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("pgcrypto step prepared=%v SQL=%s args=%#v", s.Prepared, s.Query, s.Args)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		for _, q := range []string{"SELECT digest('abc','sha256')", "SELECT hmac('abc','key','sha256')"} {
			name, args := "digest", "unknown, unknown"
			if q[7:11] == "hmac" {
				name, args = "hmac", "unknown, unknown, unknown"
			}
			s := extensionMissing(q, name, args)
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		}
		run(fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='pgcrypto'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"pgcrypto", "1.4", nil, "cryptographic functions"}}))
		run(fixture.Select("SELECT version,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='pgcrypto' AND version='1.4'", []string{"version", "superuser", "trusted", "relocatable", "schema", "requires"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "NAME", "_NAME"}, [][]any{{"1.4", true, true, true, nil, nil}}))
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoInventory(t, p, c, owner, owner)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pgcrypto'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part VERSION '1.4'", 0))
		pgcryptoInventory(t, p, c, owner, owner)
		sha := pgcryptoDigestVectors[3].abc
		run(extensionMissing("SELECT digest('abc','sha256')", "digest", "unknown, unknown"))
		pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','SHA256')", sha)
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		pgcryptoResult(t, p, c, r, "digest", "SELECT digest('abc','sha256')", sha)
		run(fixture.Exec("CREATE USER pgcrypto_owner", 0))
		// Native schema ACL SQL is unavailable; an ordinary private schema
		// already denies the reader. Keep PostgreSQL's real USAGE phase.
		if pg {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA pgcrypto_part FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(extensionMissing("SELECT digest('abc','sha256')", "digest", "unknown, unknown"))
		run(fixture.ImplicitError("SELECT pgcrypto_part.digest('abc','sha256')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgcrypto_part", Position: "8"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if pg {
			run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO pgcrypto_owner", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA pgcrypto_part OWNER TO pgcrypto_owner", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		pgcryptoResult(t, p, c, r, "digest", "SELECT digest('abc','sha256')", sha)
		pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", sha)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
