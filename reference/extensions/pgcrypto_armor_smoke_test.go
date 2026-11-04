package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoArmorFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT pgcrypto_part.armor('test'::bytea)", pgcryptoArmorTest))
		pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT pgcrypto_part.dearmor($1)", []byte("zooka"), fixture.PgcryptoArmorZooka))
		headers := fixture.PgcryptoArmorHeaders("SELECT * FROM pgcrypto_part.pgp_armor_headers($1) ORDER BY key,value", [][]any{{"Comment", "a"}, {"Version", "b"}}, pgcryptoArmorBadBody)
		headers.Bag = false
		pgcryptoArmorBoth(t, p, c, r, headers)
		pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorCorrupt("SELECT pgcrypto_part.dearmor($1)", pgcryptoArmorBadBody))
		pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorHeaders("SELECT * FROM pgcrypto_part.pgp_armor_headers(NULL::text)", [][]any{}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW armor_smoke_headers AS SELECT key,value FROM pgcrypto_part.pgp_armor_headers('"+pgcryptoArmorBadBody+"')", 0))
		view := fixture.Select("SELECT key,value FROM armor_smoke_headers ORDER BY key,value", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"Comment", "a"}, {"Version", "b"}})
		pgcryptoArmorBoth(t, p, c, r, view)
		if !pg {
			pgcryptoArmorNativeRetention(t, p, c, r)
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT pgcrypto_part.armor('test'::bytea)", pgcryptoArmorTest))
			pgcryptoArmorBoth(t, p, c, r, view)
		}
	})
}
