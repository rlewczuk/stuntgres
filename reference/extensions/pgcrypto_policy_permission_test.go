package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyPermission(t *testing.T) {
	t.Run("LiveRoleGrantRevokeAndRoutines", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"policy_reader"})
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE ROLE policy_reader", 0))
			pgcryptoPolicyRoleGrantCleanup(t, c, "policy_reader", "")
			run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO policy_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO policy_reader", 0))
			run(fixture.Exec("CREATE FUNCTION public.policy_definer_setting() RETURNS text LANGUAGE SQL SECURITY DEFINER SET pgcrypto.builtin_crypto_enabled='on' AS $$SELECT current_setting('pgcrypto.builtin_crypto_enabled')$$", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			q := "SELECT set_config($1,$2,false) AS value"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("SET ROLE policy_reader", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			pgcryptoPolicyMetadata(t, p, c, r, "off", "session")
			for _, mutation := range []string{"SET " + pgcryptoPolicyName + "='bogus'", "SET " + pgcryptoPolicyName + "=on", "RESET " + pgcryptoPolicyName, "SET " + pgcryptoPolicyName + " TO DEFAULT"} {
				for _, prepared := range []bool{false, true} {
					s := pgcryptoPolicyPermissionError(mutation)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					pgcryptoPolicyShow(t, p, c, r, "off")
				}
			}
			s := pgcryptoPolicyPermissionError(q, pgcryptoPolicyName, "bogus")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, r, s)
			run(pgcryptoPolicyText(r, "SELECT public.policy_definer_setting() AS value", "on"))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_reader", 0))
			run(fixture.Exec("SET ROLE policy_reader", 0))
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "on", pgcryptoPolicyName, "ON"))
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "off", pgcryptoPolicyName, "off"))
			s = pgcryptoPolicyInvalid(q, "bogus", pgcryptoPolicyName, "bogus")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, r, s)
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
			pgcryptoPolicyShow(t, p, c, r, "on")
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE SET ON PARAMETER pgcrypto.builtin_crypto_enabled FROM policy_reader", 0))
			run(fixture.Exec("SET ROLE policy_reader", 0))
			s = pgcryptoPolicyPermissionError(q, pgcryptoPolicyName, "on")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, r, s)
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt($1,$2)", pgcryptoPolicyOff, "foox", "NB"))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION policy_reader", 0))
			run(pgcryptoPolicyPermissionError("SET "+pgcryptoPolicyName+"='bogus'"))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			for _, value := range []string{"on", "off", "fips"} {
				run(fixture.Exec("SET "+pgcryptoPolicyName+"='"+value+"'", 0))
				pgcryptoPolicyShow(t, p, c, r, value)
			}
			pgcryptoPolicyNotices(t, notices)
		})
	})
	for _, scenario := range []struct{ name string; grantAtSet, grantAtLoad, trusted bool; want string }{
		{"OriginalUnprivilegedSetter", false, false, false, "on"},
		{"GrantBeforeReplay", false, true, false, "off"},
		{"RevokeBeforeReplay", true, false, false, "on"},
		{"GrantedSetter", true, true, false, "off"},
		{"TrustedInstallerCannotLaunderSetter", false, false, true, "on"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"policy_original_setter"})
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE ROLE policy_original_setter", 0))
				run(fixture.Exec("CREATE SCHEMA pgcrypto_part AUTHORIZATION policy_original_setter", 0))
				var database string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil { t.Fatal(err) }
				quoted := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
				pgcryptoPolicyRoleGrantCleanup(t, c, "policy_original_setter", quoted)
				run(fixture.Exec("GRANT CREATE ON DATABASE "+quoted+" TO policy_original_setter", 0))
				if scenario.grantAtSet {
					run(fixture.Exec("GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_original_setter", 0))
				}
				run(fixture.Exec("SET ROLE policy_original_setter", 0))
				run(fixture.Exec("SET "+pgcryptoPolicyName+"='off'", 0))
				pgcryptoPolicyShow(t, p, c, r, "off")
				pgcryptoPolicyHidden(t, p, c, r)
				run(fixture.Exec("RESET ROLE", 0))
				if scenario.grantAtSet != scenario.grantAtLoad {
					verb := "REVOKE SET ON PARAMETER pgcrypto.builtin_crypto_enabled FROM policy_original_setter"
					if scenario.grantAtLoad { verb = "GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_original_setter" }
					run(fixture.Exec(verb, 0))
				}
				if scenario.trusted { run(fixture.Exec("SET ROLE policy_original_setter", 0)) }
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
				if scenario.want == "on" {
					pgcryptoPolicyNotices(t, notices, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42501", Message: `permission denied to set parameter "pgcrypto.builtin_crypto_enabled"`, Context: pgcryptoPolicyCreateDigestContext})
				} else {
					pgcryptoPolicyNotices(t, notices)
				}
				pgcryptoPolicyShow(t, p, c, r, scenario.want)
				run(fixture.Exec("RESET ROLE", 0))
				pgcryptoPolicyShow(t, p, c, r, scenario.want)
			})
		})
	}
}
