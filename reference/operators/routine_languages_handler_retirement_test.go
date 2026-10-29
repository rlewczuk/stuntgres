package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesHandlerRetirement(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_retirement_owner", "rl_retirement_reader"})
	for _, combined := range []bool{false, true} {
		for _, renamed := range []bool{false, true} {
			name := "inline"
			if combined {
				name = "inline_and_validator"
			}
			if renamed {
				name += "_parked"
			} else {
				name += "_current"
			}
			t.Run(name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var baseline bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
							t.Fatal(err)
						}
					}
					// PG retirement stays in a real rollback-only transaction. Native
					// controls require autocommit; restore genuine pristine bootstrap
					// bytes even when setup or the expected native-red fails early.
					defer func() {
						routineLanguagesCleanup(t, c)()
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if !b.PG {
							if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(baseline.Bytes())); err != nil {
								t.Error("handler retirement bootstrap cleanup", err)
							}
						}
						stockOwner := "postgres"
						if b.PG {
							stockOwner = "stuntgres"
						}
						var valid bool
						q := "SELECT l.lanispl AND l.lanpltrusted AND l.lanacl IS NULL AND l.lanowner=(SELECT oid FROM pg_roles WHERE rolname='" + stockOwner + "') AND l.lanplcallfoid=h.oid AND l.laninline=i.oid AND l.lanvalidator=v.oid FROM pg_language l JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace JOIN pg_depend d ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='plpgsql' AND e.extname='plpgsql' AND d.deptype='e'"
						if err := c.QueryRowContext(ctx, q).Scan(&valid); err != nil || !valid {
							t.Errorf("handler retirement cleanup did not restore actual bootstrap: %v %v", valid, err)
						}
					}()
					if b.PG {
						userOperatorExec(t, p, b, c, "BEGIN")
					}
					userOperatorExec(t, p, b, c, "CREATE ROLE rl_retirement_owner", "CREATE ROLE rl_retirement_reader",
						"CREATE FUNCTION rl_retirement_checked(x integer) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN x+2; END'")
					original := routineLanguagesOID(t, p, c, "plpgsql")
					language := "plpgsql"
					if renamed {
						userOperatorExec(t, p, b, c, "ALTER LANGUAGE plpgsql RENAME TO rl_retired_pl")
						language = "rl_retired_pl"
					}
					definition := "CREATE OR REPLACE TRUSTED LANGUAGE " + language + routineLanguageCall
					if !combined {
						definition += " VALIDATOR pg_catalog.plpgsql_validator"
					}
					userOperatorExec(t, p, b, c, definition,
						"ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)",
						"DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)")
					if combined {
						userOperatorExec(t, p, b, c, "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)", "DROP FUNCTION pg_catalog.plpgsql_validator(oid)")
					}
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION rl_retirement_fn(x integer) RETURNS integer LANGUAGE "+language+" AS 'BEGIN RETURN x+1; END'",
						"REVOKE USAGE ON LANGUAGE "+language+" FROM PUBLIC",
						"GRANT USAGE ON LANGUAGE "+language+" TO rl_retirement_reader WITH GRANT OPTION",
						"ALTER LANGUAGE "+language+" OWNER TO rl_retirement_owner",
						"GRANT USAGE,CREATE ON SCHEMA public TO rl_retirement_reader",
						"ALTER FUNCTION rl_retirement_checked(integer) OWNER TO rl_retirement_reader",
						"ALTER FUNCTION rl_retirement_fn(integer) OWNER TO rl_retirement_reader",
						"CREATE TABLE rl_retirement_data(id integer,v integer DEFAULT rl_retirement_fn(4) CONSTRAINT rl_retirement_positive CHECK(rl_retirement_fn(v)>0))")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_retirement_data(id) VALUES(1)", 1))
					userOperatorExec(t, p, b, c, "CREATE VIEW rl_retirement_view AS SELECT rl_retirement_fn(v) AS v FROM rl_retirement_data")
					if got := routineLanguagesOID(t, p, c, language); got != original {
						t.Fatal("retirement changed installed language identity", original, got)
					}
					// A reached error must not abort the rollback-only reference arm
					// before its next independently asserted diagnostic/metadata query.
					reject := func(c *sql.Conn, b fixture.Backend, q string, d fixture.Diagnostic, query bool) {
						for _, prepared := range []bool{false, true} {
							if b.PG {
								userOperatorExec(t, p, b, c, "SAVEPOINT rl_retirement_error")
							}
							step := fixture.ErrorDiagnostic(q, d)
							step.Prepared, step.SelectRows, step.ErrorStage = prepared, query, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
							if b.PG {
								userOperatorExec(t, p, b, c, "ROLLBACK TO SAVEPOINT rl_retirement_error", "RELEASE SAVEPOINT rl_retirement_error")
							}
						}
					}
					check := func(c *sql.Conn, b fixture.Backend) {
						validator := "l.lanvalidator=v.oid"
						validatorJoin := " JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace"
						edges := int64(2)
						if combined {
							validator, validatorJoin, edges = "l.lanvalidator=0", "", 1
						}
						plpgsqlValue(t, p, b, c, "SELECT l.lanispl AND l.lanpltrusted AND l.laninline=0 AND "+validator+" AND l.lanplcallfoid=h.oid AND l.lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_retirement_owner') AS v FROM pg_language l JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace"+validatorJoin+" WHERE l.lanname='"+language+"'", "BOOL", true)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='plpgsql_inline_handler' AND pronamespace='pg_catalog'::regnamespace", "INT8", int64(0))
						if combined {
							plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='plpgsql_validator' AND pronamespace='pg_catalog'::regnamespace", "INT8", int64(0))
						}
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid WHERE l.lanname='"+language+"' AND d.refclassid='pg_proc'::regclass AND d.deptype='n'", "INT8", edges)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='"+language+"' AND e.extname='plpgsql' AND d.deptype='e'", "INT8", int64(1))
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_proc p ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN pg_language l ON d.refclassid='pg_language'::regclass AND d.refobjid=l.oid WHERE l.lanname='"+language+"' AND p.proname='rl_retirement_fn' AND p.prolang=l.oid AND d.deptype='n'", "INT8", int64(1))
						acl := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='"+language+"'", []string{"acl"}, []string{"TEXT"}, [][]any{{"rl_retirement_owner=U/rl_retirement_owner"}, {"rl_retirement_reader=U*/rl_retirement_owner"}})
						acl.Bag = true
						fixture.RunStep(t, p.Ctx, c, b.Recorder, acl)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(acl))
						plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','"+language+"','USAGE') AS v", "BOOL", false)
						plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_retirement_reader','"+language+"','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('rl_retirement_fn','rl_retirement_checked') AND proowner=(SELECT oid FROM pg_roles WHERE rolname='rl_retirement_reader')", "INT8", int64(2))
						for _, tc := range []struct {
							name, body    string
							first, second int64
						}{{"rl_retirement_fn", "BEGIN RETURN x+1; END", 5, 10}, {"rl_retirement_checked", "BEGIN RETURN x+2; END", 6, 11}} {
							parametersValue(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "v", "INT4", tc.first, 4)
							parametersHeld(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{tc.first, tc.second})
							definition := "CREATE OR REPLACE FUNCTION public." + tc.name + "(x integer)\n RETURNS integer\n LANGUAGE " + language + "\nAS $function$" + tc.body + "$function$\n"
							for _, inq := range []struct{ name, want string }{{"pg_get_functiondef", definition}, {"pg_get_function_arguments", "x integer"}, {"pg_get_function_identity_arguments", "x integer"}, {"pg_get_function_result", "integer"}} {
								step := fixture.DeparseRelationsTextStep(t, b, "SELECT "+inq.name+"('"+tc.name+"(integer)'::regprocedure) AS definition", inq.want)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
							}
						}
						plpgsqlValue(t, p, b, c, "SELECT v FROM rl_retirement_data WHERE id=1", "INT4", int64(5))
						plpgsqlValue(t, p, b, c, "SELECT v FROM rl_retirement_view", "INT4", int64(6))
						reject(c, b, "DO LANGUAGE "+language+" $$BEGIN END$$", fixture.ControlsReferenceDiagnostic("0A000", `language "`+language+`" does not support inline code execution`), false)
						if !combined {
							q := "CREATE FUNCTION rl_retirement_rejected() RETURNS integer LANGUAGE " + language + " AS 'BEGIN RETURN 1;'"
							d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
							d.Position = strconv.Itoa(strings.Index(q, "BEGIN RETURN 1;") + 16)
							reject(c, b, q, d, false)
							plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_retirement_rejected'", "INT8", int64(0))
						}
					}
					check(c, b)
					if !b.PG {
						routineLanguagesExtensionReplay(t, p, b, c, []string{"rl_retirement_owner", "rl_retirement_reader"}, "rl_retirement_fn", check)
					}
				})
			})
		}
	}
}
