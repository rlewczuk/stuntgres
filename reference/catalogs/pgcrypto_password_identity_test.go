package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPasswordIdentity(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, v := range []struct{ q, name, args string }{
			{"SELECT crypt('foox','salt')", "crypt", "unknown, unknown"},
			{"SELECT gen_salt('bf')", "gen_salt", "unknown"},
			{"SELECT gen_salt('bf',4)", "gen_salt", "unknown, integer"},
		} {
			run(extensionMissing(v.q, v.name, v.args))
		}
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		pgcryptoInventory(t, p, c, owner, owner)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE nspname='pgcrypto_part' AND proname IN ('crypt','gen_salt')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoInventory(t, p, c, owner, owner)
		run(extensionMissing("SELECT crypt('foox','salt')", "crypt", "unknown, unknown"))
		pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt('foox',$1) AS value", pgcryptoPasswordFoox, pgcryptoPasswordSalt)
		pgcryptoReplayOverloads(t, p, c, r)
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		pgcryptoPasswordText(t, p, c, r, "SELECT crypt('foox',$1) AS value", pgcryptoPasswordFoox, pgcryptoPasswordSalt)
		run(fixture.Exec("CREATE USER pgcrypto_owner", 0))
		if pg {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA pgcrypto_part FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(extensionMissing("SELECT crypt('foox','salt')", "crypt", "unknown, unknown"))
		run(fixture.ImplicitError("SELECT pgcrypto_part.gen_salt('bf')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgcrypto_part", Position: "8"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if pg {
			run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO pgcrypto_owner", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA pgcrypto_part OWNER TO pgcrypto_owner", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		pgcryptoPasswordText(t, p, c, r, "SELECT crypt('foox',$1) AS value", pgcryptoPasswordFoox, pgcryptoPasswordSalt)
		pgcryptoPasswordGenerated(t, p, c, r, "SELECT gen_salt('bf',4)", "crypt", 4)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
