package reference_test

import (
	"crypto/fips140"
	"database/sql"
	"os"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"policy_control_reader"})
	pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		pgcryptoPolicyUnseen(t, p, c, r)
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoPolicyMetadata(t, p, c, r, "on", "default")
		run(fixture.Exec("CREATE TABLE policy_smoke(id integer,p text DEFAULT 'foox',h text DEFAULT pgcrypto_part.crypt('foox','NB'))", 0))
		run(fixture.Exec("CREATE VIEW policy_smoke_view AS SELECT id,pgcrypto_part.crypt(p,'NB') AS value FROM policy_smoke", 0))
		run(fixture.Exec("INSERT INTO policy_smoke(id) VALUES(1)", 1))
		q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E", "foox", "NB"))
		run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "foox", "NB"))
		run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt(p,'NB') FROM policy_smoke", pgcryptoPolicyOff))
		for _, s := range []fixture.Step{
			pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1)", pgcryptoPolicyOff, "des"),
			pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1,$2)", pgcryptoPolicyOff, "des", 25),
			pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "p", "$5$rounds=10$!"),
		} { run(s); run(fixture.Prepared(s)) }
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, nil, nil, "malformed"))
		run(pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt($1) AS value", nil, nil))
		run(pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt($1,$2) AS value", nil, nil, -1))
		run(pgcryptoPolicyInvalid("SET "+pgcryptoPolicyName+"='bogus'", "bogus"))
		pgcryptoPolicyShow(t, p, c, r, "off")
		pgcryptoPolicyActivate(t, p, c, r)
		pgcryptoPolicyNotices(t, notices)
		run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E", "foox", "NB"))
		wantMode := false // The pinned ordinary PostgreSQL provider is not enabled.
		if !pg {
			wantMode = fips140.Enabled()
			for _, option := range strings.Split(os.Getenv("GODEBUG"), ",") {
				if option == "fips140=on" && !wantMode || option == "fips140=off" && wantMode {
					t.Fatal("actual native FIPS mode disagrees with process startup", option, wantMode)
				}
			}
		}
		run(fixture.Select("SELECT pgcrypto_part.fips_mode() AS value", []string{"value"}, []string{"BOOL"}, [][]any{{wantMode}}))
		run(fixture.Exec("SET "+pgcryptoPolicyName+"=fips", 0))
		if wantMode {
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyFailure(q, pgcryptoPolicyFIPS, "foox", "NB"))
			for _, call := range []fixture.Step{pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1)", pgcryptoPolicyFIPS, "des"), pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1,$2)", pgcryptoPolicyFIPS, "sha256crypt", 1000)} { run(call) }
		} else {
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E", "foox", "NB"))
		}
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, nil, nil, "bad"))
		owner := "postgres"
		if pg { owner = "stuntgres" }
		pgcryptoRandomInventory(t, p, c, pg, owner)
		if !pg {
			run(fixture.Exec("CREATE ROLE policy_control_reader", 0))
			pgcryptoPolicyRoleGrantCleanup(t, c, "policy_control_reader", "")
			run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO policy_control_reader", 0))
			run(fixture.Exec("GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_control_reader", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			stored := fixture.Select("SELECT id,p,h FROM policy_smoke ORDER BY id", []string{"id", "p", "h"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "foox", "NB53EGGqrrb5E"}})
			run(stored)
			script := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil { t.Fatal(err) }
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(stored)
			run(pgcryptoPolicyFailure("SELECT value FROM policy_smoke_view", pgcryptoPolicyOff))
			pgcryptoPolicyControlACL(t, p, c, r)
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=on", 0))
			run(fixture.Exec("INSERT INTO policy_smoke(id) VALUES(2)", 1))
			run(fixture.Select("SELECT id,value FROM policy_smoke_view ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "NB53EGGqrrb5E"}, {int64(2), "NB53EGGqrrb5E"}}))
			pgcryptoRandomInventory(t, p, c, false, owner)
		}
		pgcryptoPolicyNotices(t, notices)
	})
}
