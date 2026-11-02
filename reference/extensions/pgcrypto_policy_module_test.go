package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyModuleLifecycle(t *testing.T) {
	for _, value := range []string{"off", "bogus"} {
		t.Run("CreatePromotion_"+value, func(t *testing.T) {
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				pgcryptoPolicyUnseen(t, p, c, r)
				run(fixture.Exec("SET "+pgcryptoPolicyName+"='"+value+"'", 0))
				pgcryptoPolicyShow(t, p, c, r, value)
				pgcryptoPolicyHidden(t, p, c, r)
				run(fixture.Exec("SET pgcrypto.typo='keep no such member'", 0))
				run(fixture.Exec("SET policy_other.value='unrelated'", 0))
				run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
				var expected []fixture.Diagnostic
				want := value
				if value == "bogus" {
					want = "on"
					expected = append(expected, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `invalid value for parameter "pgcrypto.builtin_crypto_enabled": "bogus"`, Hint: "Available values: on, off, fips.", Context: pgcryptoPolicyCreateDigestContext})
				}
				expected = append(expected, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42602", Message: `invalid configuration parameter name "pgcrypto.typo", removing it`, Detail: `"pgcrypto" is now a reserved prefix.`, Context: pgcryptoPolicyCreateDigestContext})
				pgcryptoPolicyNotices(t, notices, expected...)
				pgcryptoPolicyShow(t, p, c, r, want)
				run(fixture.ImplicitError("SHOW pgcrypto.typo", fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "pgcrypto.typo"`}))
				run(fixture.ImplicitError("SET pgcrypto.typo='new'", fixture.Diagnostic{Code: "42602", Message: `invalid configuration parameter name "pgcrypto.typo"`, Detail: `"pgcrypto" is a reserved prefix.`}))
				run(pgcryptoPolicyText(r, "SELECT current_setting('policy_other.value') AS value", "unrelated"))
				if want == "off" {
					run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt($1,$2)", pgcryptoPolicyOff, "foox", "NB"))
				} else {
					run(pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt($1,$2) AS value", "NB53EGGqrrb5E", "foox", "NB"))
				}
				pgcryptoPolicyActivate(t, p, c, r)
				pgcryptoPolicyActivate(t, p, c, r)
				pgcryptoPolicyNotices(t, notices)
				run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
				pgcryptoPolicyShow(t, p, c, r, "on")
			})
		})
	}
	t.Run("PeerPhysicalAndBorrowerReuse", func(t *testing.T) {
		pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			var peerNotices []fixture.Diagnostic
			pool, peer, peerRecord := fixture.NoticeReferencePool(t, p, pg, &peerNotices)
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			identity := fixture.SettingsPhysical(t, peer)
			pgcryptoPolicyUnseen(t, p, c, r)
			pgcryptoPolicyUnseen(t, p, peer, peerRecord)
			run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
			run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			pgcryptoPolicyUnseen(t, p, peer, peerRecord)
			fixture.RunStep(t, p.Ctx, peer, peerRecord, fixture.Exec("SET "+pgcryptoPolicyName+"='off'", 0))
			pgcryptoPolicyHidden(t, p, peer, peerRecord)
			pgcryptoPolicyActivate(t, p, peer, peerRecord)
			pgcryptoPolicyShow(t, p, peer, peerRecord, "off")
			fixture.RunStep(t, p.Ctx, peer, peerRecord, pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1)", pgcryptoPolicyOff, "des"))
			pgcryptoPolicyShow(t, p, c, r, "on")
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			reused, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reused.Close()
			if fixture.SettingsPhysical(t, reused) != identity {
				t.Fatal("single-slot pool changed physical connection")
			}
			pgcryptoPolicyShow(t, p, reused, peerRecord, "off")
			var freshNotices []fixture.Diagnostic
			_, fresh, record := fixture.NoticeReferencePool(t, p, pg, &freshNotices)
			pgcryptoPolicyUnseen(t, p, fresh, record)
			pgcryptoPolicyActivate(t, p, fresh, record)
			pgcryptoPolicyShow(t, p, fresh, record, "on")
			pgcryptoPolicyNotices(t, &peerNotices)
			pgcryptoPolicyNotices(t, &freshNotices)
			pgcryptoPolicyNotices(t, notices)
		})
	})
	for _, scenario := range []struct{ name, sql string; loaded bool }{
		{"StrictLiteralNull", "SELECT pgcrypto_part.crypt(NULL::text,'NB') AS value", false},
		{"RuntimeNull", "SELECT pgcrypto_part.crypt(p,'NB') AS value FROM policy_module_input", true},
		{"ZeroRows", "SELECT pgcrypto_part.crypt(p,'NB') AS value FROM policy_module_input WHERE id=9", true},
		{"ConstantFalseCase", "SELECT CASE WHEN false THEN pgcrypto_part.crypt('foox','NB') ELSE NULL::text END AS value", false},
		{"RuntimeFalseCase", "SELECT CASE WHEN id=9 THEN pgcrypto_part.crypt(p,'NB') ELSE NULL::text END AS value FROM policy_module_input", true},
	} {
		t.Run("CallInitialization_"+scenario.name, func(t *testing.T) {
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				var installerNotices []fixture.Diagnostic
				_, installer, ir := fixture.NoticeReferencePool(t, p, pg, &installerNotices)
				fixture.RunStep(t, p.Ctx, installer, ir, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				fixture.RunStep(t, p.Ctx, installer, ir, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE policy_module_input(id integer,p text)", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO policy_module_input VALUES(1,NULL)", 1))
				pgcryptoPolicyUnseen(t, p, c, r)
				stmt, err := c.PrepareContext(p.Ctx, scenario.sql)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				pgcryptoPolicyUnseen(t, p, c, r)
				step := pgcryptoPolicyText(r, scenario.sql, nil)
				if scenario.name == "ZeroRows" {
					step.Rows = [][]any{}
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: scenario.sql}, r, step)
				if scenario.loaded {
					pgcryptoPolicyMetadata(t, p, c, r, "on", "default")
				} else {
					pgcryptoPolicyUnseen(t, p, c, r)
				}
				pgcryptoPolicyNotices(t, notices)
			})
		})
	}
	t.Run("PromotionJournalAndSavepoint", func(t *testing.T) {
		pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"='off'", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"='fips'", 0))
			run(fixture.Exec("SAVEPOINT before_module", 0))
			run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"='on'", 0))
			run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			pgcryptoPolicyShow(t, p, c, r, "on")
			run(fixture.Exec("ROLLBACK TO before_module", 0))
			pgcryptoPolicyShow(t, p, c, r, "fips")
			run(fixture.Exec("ROLLBACK", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			// Loading is physical, even though the extension catalog row rolled back.
			run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pgcrypto'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(pgcryptoPolicyInvalid("SET "+pgcryptoPolicyName+"='bogus'", "bogus"))
			run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			run(fixture.Exec("DROP EXTENSION pgcrypto", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt($1,$2)", pgcryptoPolicyOff, "foox", "NB"))
			pgcryptoPolicyNotices(t, notices)
		})
	})
	for _, exception := range []bool{false, true} {
		name := "InvocationImagePromotion"
		if exception { name = "ExceptionRollbackKeepsPhysicalActivation" }
		t.Run(name, func(t *testing.T) {
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				var installerNotices []fixture.Diagnostic
				_, installer, record := fixture.NoticeReferencePool(t, p, pg, &installerNotices)
				fixture.RunStep(t, p.Ctx, installer, record, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				fixture.RunStep(t, p.Ctx, installer, record, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
				run(fixture.Exec("SET "+pgcryptoPolicyName+"='off'", 0))
				body := "BEGIN PERFORM pgcrypto_part.digest('abc','sha256'); RETURN current_setting('pgcrypto.builtin_crypto_enabled'); END"
				want := "on"
				if exception {
					body = "BEGIN BEGIN PERFORM pgcrypto_part.digest('abc','sha256'); RAISE EXCEPTION 'rollback invocation'; EXCEPTION WHEN OTHERS THEN RETURN current_setting('pgcrypto.builtin_crypto_enabled'); END; RETURN current_setting('pgcrypto.builtin_crypto_enabled'); END"
				}
				run(fixture.Exec("CREATE FUNCTION public.policy_module_invocation() RETURNS text LANGUAGE plpgsql SET pgcrypto.builtin_crypto_enabled='on' AS $$"+body+"$$", 0))
				pgcryptoPolicyHidden(t, p, c, r)
				run(pgcryptoPolicyText(r, "SELECT public.policy_module_invocation() AS value", want))
				pgcryptoPolicyShow(t, p, c, r, "off")
				pgcryptoPolicyMetadata(t, p, c, r, "off", "session")
				run(pgcryptoPolicyInvalid("SET "+pgcryptoPolicyName+"='bogus'", "bogus"))
				run(pgcryptoPolicyFailure("SELECT pgcrypto_part.crypt($1,$2)", pgcryptoPolicyOff, "foox", "NB"))
				pgcryptoPolicyNotices(t, notices)
			})
		})
	}
	for _, scenario := range []struct{ name, initial, local, after string }{
		{"InvalidSavedValue", "bogus", "off", "on"},
		{"InvalidCurrentValue", "off", "bogus", "off"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				run(fixture.Exec("SET "+pgcryptoPolicyName+"='"+scenario.initial+"'", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"='"+scenario.local+"'", 0))
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
				warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `invalid value for parameter "pgcrypto.builtin_crypto_enabled": "bogus"`, Hint: "Available values: on, off, fips.", Context: pgcryptoPolicyCreateDigestContext}
				pgcryptoPolicyNotices(t, notices, warning)
				run(fixture.Exec("COMMIT", 0))
				pgcryptoPolicyShow(t, p, c, r, scenario.after)
				run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
				pgcryptoPolicyShow(t, p, c, r, "on")
			})
		})
	}
	t.Run("DistinctIdenticalStackAssignments", func(t *testing.T) {
		pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"='off'", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"='bogus'", 0))
			run(fixture.Exec("SAVEPOINT repeated_policy", 0))
			run(fixture.Exec("SET LOCAL "+pgcryptoPolicyName+"='bogus'", 0))
			run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
			// REL_18_6 reapply_stacked_values replays both LOCAL actions even
			// though their text and original setter are identical.
			warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `invalid value for parameter "pgcrypto.builtin_crypto_enabled": "bogus"`, Hint: "Available values: on, off, fips.", Context: pgcryptoPolicyCreateDigestContext}
			pgcryptoPolicyNotices(t, notices, warning, warning)
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("ROLLBACK TO repeated_policy", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("COMMIT", 0))
			pgcryptoPolicyShow(t, p, c, r, "off")
			run(fixture.Exec("RESET "+pgcryptoPolicyName, 0))
			pgcryptoPolicyShow(t, p, c, r, "on")
			pgcryptoPolicyNotices(t, notices)
		})
	})
	for _, stage := range []string{"PreparedCreate", "EarlyFailure", "LateMemberFailure", "SkippedCreate"} {
		t.Run(stage, func(t *testing.T) {
			pgcryptoPolicyCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
				switch stage {
				case "PreparedCreate":
					q := "CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil { t.Fatal(err) }
					defer stmt.Close()
					pgcryptoPolicyUnseen(t, p, c, r)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Exec(q, 0))
					pgcryptoPolicyShow(t, p, c, r, "on")
				case "EarlyFailure":
					run(fixture.ImplicitError("CREATE EXTENSION pgcrypto SCHEMA no_policy_schema", fixture.Diagnostic{Code: "3F000", Message: `schema "no_policy_schema" does not exist`}))
					pgcryptoPolicyUnseen(t, p, c, r)
				case "LateMemberFailure":
					run(fixture.Exec("CREATE FUNCTION pgcrypto_part.crypt(text,text) RETURNS text LANGUAGE SQL IMMUTABLE AS $$SELECT $1$$", 0))
					run(fixture.ImplicitError("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", fixture.Diagnostic{Code: "42723", Message: `function "crypt" already exists with same argument types`, Context: pgcryptoPolicyCreateCryptContext}))
					pgcryptoPolicyShow(t, p, c, r, "on")
					run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pgcrypto'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				case "SkippedCreate":
					var peerNotices []fixture.Diagnostic
					_, peer, pr := fixture.NoticeReferencePool(t, p, pg, &peerNotices)
					fixture.RunStep(t, p.Ctx, peer, pr, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
					run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto SCHEMA pgcrypto_part", 0))
					pgcryptoPolicyNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "pgcrypto" already exists, skipping`})
					pgcryptoPolicyUnseen(t, p, c, r)
				}
				pgcryptoPolicyNotices(t, notices)
			})
		})
	}
}
