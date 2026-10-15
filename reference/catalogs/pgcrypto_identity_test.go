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
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,proname,p.oid::regprocedure::text,prorettype,provolatile,proisstrict,proparallel,rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE pronamespace=$1 AND proname IN ('digest','hmac','crypt','gen_salt')", ns)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{
		"digest:pgcrypto_part.digest(text,text)":        false,
		"digest:pgcrypto_part.digest(bytea,text)":       false,
		"hmac:pgcrypto_part.hmac(text,text,text)":       false,
		"hmac:pgcrypto_part.hmac(bytea,bytea,text)":     false,
		"crypt:pgcrypto_part.crypt(text,text)":          false,
		"gen_salt:pgcrypto_part.gen_salt(text)":         false,
		"gen_salt:pgcrypto_part.gen_salt(text,integer)": false,
	}
	ids := map[int64]bool{}
	for rows.Next() {
		var id, ret int64
		var name, signature, vol, parallel, memberOwner string
		var strict bool
		if err := rows.Scan(&id, &name, &signature, &ret, &vol, &strict, &parallel, &memberOwner); err != nil {
			t.Fatal(err)
		}
		key := name + ":" + signature
		seen, ok := expected[key]
		wantRet, wantVol := int64(17), "i"
		if name == "crypt" || name == "gen_salt" {
			wantRet = 25
		}
		if name == "gen_salt" {
			wantVol = "v"
		}
		if !ok || seen || id <= 0 || ids[id] || ret != wantRet || vol != wantVol || !strict || parallel != "s" || memberOwner != expectedMemberOwner {
			t.Fatalf("procedure identity %d %s %d %s %v %s %s; expected member owner %s", id, key, ret, vol, strict, parallel, memberOwner, expectedMemberOwner)
		}
		expected[key] = true
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for key, seen := range expected {
		if !seen {
			t.Fatal("missing signature", key)
		}
	}
	for id := range ids {
		var count int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", id, ext).Scan(&count); err != nil || count != 1 {
			t.Fatal("actual procedure membership", id, count, err)
		}
	}
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
