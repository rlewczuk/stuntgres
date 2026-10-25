package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoAESIdentity(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, fn := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
			args := "unknown, unknown, unknown"
			q := "SELECT " + fn + "('foo','key','aes')"
			if fn == "encrypt_iv" || fn == "decrypt_iv" {
				args += ", unknown"
				q = "SELECT " + fn + "('foo','key','iv','aes')"
			}
			s := extensionMissing(q, fn, args)
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		}
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,prorettype,provolatile,proisstrict,proparallel FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE nspname='pgcrypto_part' AND proname IN ('encrypt','decrypt','encrypt_iv','decrypt_iv')")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{"pgcrypto_part.encrypt(bytea,bytea,text)": false, "pgcrypto_part.decrypt(bytea,bytea,text)": false, "pgcrypto_part.encrypt_iv(bytea,bytea,bytea,text)": false, "pgcrypto_part.decrypt_iv(bytea,bytea,bytea,text)": false}
		ids := map[int64]bool{}
		for rows.Next() {
			var id, ret int64
			var signature, vol, parallel string
			var strict bool
			if err := rows.Scan(&id, &signature, &ret, &vol, &strict, &parallel); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			seen, ok := want[signature]
			if !ok || seen || id <= 0 || ids[id] || ret != 17 || vol != "i" || !strict || parallel != "s" {
				rows.Close()
				t.Fatal("raw AES identity", id, signature, ret, vol, strict, parallel)
			}
			want[signature] = true
			ids[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		for signature, seen := range want {
			if !seen {
				t.Fatal("missing installed signature", signature)
			}
		}
		for id := range ids {
			run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e' AND extname='pgcrypto'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, id))
		}
		run(extensionMissing("SELECT encrypt('foo','key','aes')", "encrypt", "unknown, unknown, unknown"))
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT pgcrypto_part.encrypt('foo','0123456789','aes')", "f397f03d2819b7172b68d0706fda4693")
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		run(fixture.Exec("CREATE USER pgcrypto_owner", 0))
		if pg {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA pgcrypto_part FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(extensionMissing("SELECT encrypt('foo','key','aes')", "encrypt", "unknown, unknown, unknown"))
		run(fixture.ImplicitError("SELECT pgcrypto_part.encrypt('foo','key','aes')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgcrypto_part", Position: "8"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if pg {
			run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO pgcrypto_owner", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA pgcrypto_part OWNER TO pgcrypto_owner", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT encrypt_iv('foo','0123456','abcd','aes')", "2c24cb7da91d6d5699801268b0f5adad")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
