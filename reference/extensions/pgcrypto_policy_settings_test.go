package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicySettings(t *testing.T) {
	t.Run("EnumMetadataAndMutation", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			pgcryptoPolicyShow(t, p, c, r, "on")
			pgcryptoPolicyMetadata(t, p, c, r, "on", "default")
			for _, v := range []struct{ input, value string }{{"OFF", "off"}, {"FIPS", "fips"}, {"On", "on"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("SET "+pgcryptoPolicyName+"='"+v.input+"'", 0)
					s.Prepared = prepared
					run(s)
					pgcryptoPolicyShow(t, p, c, r, v.value)
					s = pgcryptoPolicyText(r, "SELECT set_config($1,$2,false) AS value", v.value, pgcryptoPolicyName, v.input)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			pgcryptoPolicyMetadata(t, p, c, r, "off", "session")
			for _, value := range []string{"bogus", "true", "false", "yes", "1", "o", "fi", " fips ", ""} {
				for _, prepared := range []bool{false, true} {
					s := pgcryptoPolicyInvalid("SET "+pgcryptoPolicyName+"='"+value+"'", value)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					s = pgcryptoPolicyInvalid("SELECT set_config($1,$2,false)", value, pgcryptoPolicyName, value)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
					pgcryptoPolicyShow(t, p, c, r, "off")
					run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt($1,$2)", pgcryptoPolicyOff, "foox", "NB"))
				}
			}
			for _, reset := range []string{"RESET " + pgcryptoPolicyName, "SET " + pgcryptoPolicyName + " TO DEFAULT", "RESET ALL"} {
				run(fixture.Exec(reset, 0))
				pgcryptoPolicyShow(t, p, c, r, "on")
				pgcryptoPolicyMetadata(t, p, c, r, "on", "default")
				run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			}
			// NULL values reset; NULL names are errors without changing the setting.
			run(pgcryptoPolicyText(r, "SELECT set_config($1,NULL,false) AS value", "on", pgcryptoPolicyName))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT set_config(NULL,'off',false) AS value", fixture.Diagnostic{Code: "22004", Message: "SET requires parameter name"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				run(s)
				pgcryptoPolicyShow(t, p, c, r, "on")
			}
			run(pgcryptoPolicyText(r, "SELECT current_setting(NULL) AS value", nil))
			pgcryptoPolicyShow(t, p, c, r, "on")
			run(pgcryptoPolicyText(r, "SELECT set_config($1,'off',NULL) AS value", "off", pgcryptoPolicyName))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
			pgcryptoPolicyShow(t, p, c, r, "on")
			pgcryptoPolicyNotices(t, notices)
		})
	})
	t.Run("TransactionsAndRoutineConfiguration", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			run(fixture.Exec("SAVEPOINT policy_value", 0))
			run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"=fips", 0))
			pgcryptoPolicyShow(t, p, c, r, "fips")
			run(fixture.Exec("ROLLBACK TO policy_value", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("COMMIT", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("BEGIN", 0))
			run(pgcryptoPolicyText(r, "SELECT set_config($1,'on',true) AS value", "on", pgcryptoPolicyName))
			run(fixture.Exec("COMMIT", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
			pgcryptoPolicyShow(t, p, c, r, "on")
			run(fixture.Exec("ROLLBACK", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("CREATE FUNCTION public.policy_local_setting() RETURNS text LANGUAGE SQL SET pgcrypto.builtin_crypto_enabled='on' AS $$SELECT current_setting('pgcrypto.builtin_crypto_enabled')$$", 0))
			run(pgcryptoPolicyText(r, "SELECT public.policy_local_setting() AS value", "on"))
			run(fixture.Prepared(pgcryptoPolicyText(r, "SELECT public.policy_local_setting() AS value", "on")))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("CREATE FUNCTION public.policy_exception_setting() RETURNS text LANGUAGE plpgsql AS $$ BEGIN BEGIN PERFORM set_config('pgcrypto.builtin_crypto_enabled','on',false); RAISE EXCEPTION 'restore'; EXCEPTION WHEN OTHERS THEN RETURN current_setting('pgcrypto.builtin_crypto_enabled'); END; RETURN current_setting('pgcrypto.builtin_crypto_enabled'); END $$", 0))
			run(pgcryptoPolicyText(r, "SELECT public.policy_exception_setting() AS value", "off"))
			pgcryptoPolicyShow(t, p, c, r, "off")
			pgcryptoPolicyNotices(t, notices)
		})
	})
}
