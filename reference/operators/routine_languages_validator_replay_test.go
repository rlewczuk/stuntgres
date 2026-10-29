package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Capture after validator addition: capturing the earlier call-only language
// cannot distinguish replay of a declaration's admission from its current route.
func TestReferenceRoutineLanguagesValidatorReplay(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_validator_owner", "rl_validator_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			// Run with independent bounded cleanup even when setup or a replay
			// assertion stops before extension membership has been installed.
			routineLanguagesCleanup(t, c)()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var members int64
			q := "SELECT count(*) FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='rl_validator_replay' AND e.extname='plpgsql' AND d.deptype='e'"
			if err := c.QueryRowContext(ctx, q).Scan(&members); err != nil {
				t.Errorf("validator replay cleanup membership lookup: %v", err)
			} else if members != 0 {
				if _, err := c.ExecContext(ctx, "ALTER EXTENSION plpgsql DROP LANGUAGE rl_validator_replay"); err != nil {
					t.Errorf("validator replay cleanup detach: %v", err)
				}
			}
			routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_validator_replay CASCADE")()
			for _, role := range []string{"rl_validator_owner", "rl_validator_reader"} {
				var exists bool
				if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
					t.Errorf("validator replay cleanup role lookup %s: %v", role, err)
				} else if exists {
					routineLanguagesCleanup(t, c, "DROP OWNED BY "+role+" CASCADE")()
				}
			}
		}()
		userOperatorExec(t, p, b, c,
			"CREATE ROLE rl_validator_owner", "CREATE ROLE rl_validator_reader",
			"CREATE TRUSTED LANGUAGE rl_validator_replay"+routineLanguageCall,
			"CREATE FUNCTION rl_validator_bad() RETURNS integer LANGUAGE rl_validator_replay AS 'BEGIN RETURN 1;'",
			"CREATE FUNCTION rl_validator_result() RETURNS cstring LANGUAGE rl_validator_replay AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_validator_argument(x cstring) RETURNS integer LANGUAGE rl_validator_replay AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_validator_good(x integer) RETURNS integer LANGUAGE rl_validator_replay AS 'BEGIN RETURN x+1; END'")
		before := routineLanguagesOID(t, p, c, "rl_validator_replay")
		userOperatorExec(t, p, b, c,
			"CREATE OR REPLACE TRUSTED LANGUAGE rl_validator_replay"+routineLanguageFull,
			"REVOKE USAGE ON LANGUAGE rl_validator_replay FROM PUBLIC",
			"GRANT USAGE ON LANGUAGE rl_validator_replay TO rl_validator_reader WITH GRANT OPTION",
			"ALTER LANGUAGE rl_validator_replay OWNER TO rl_validator_owner",
			"ALTER EXTENSION plpgsql ADD LANGUAGE rl_validator_replay",
			"CREATE FUNCTION rl_validator_checked(x integer) RETURNS integer LANGUAGE rl_validator_replay AS 'BEGIN RETURN x+1; END'",
			"GRANT USAGE,CREATE ON SCHEMA public TO rl_validator_reader",
			"ALTER FUNCTION rl_validator_good(integer) OWNER TO rl_validator_reader",
			"ALTER FUNCTION rl_validator_checked(integer) OWNER TO rl_validator_reader",
			"CREATE TABLE rl_validator_data(id integer,v integer DEFAULT rl_validator_good(4) CONSTRAINT rl_validator_positive CHECK (rl_validator_good(v)>0))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_validator_data(id) VALUES(1)", 1))
		userOperatorExec(t, p, b, c, "CREATE VIEW rl_validator_view AS SELECT rl_validator_good(v) AS v FROM rl_validator_data")
		if got := routineLanguagesOID(t, p, c, "rl_validator_replay"); got != before {
			t.Fatalf("validator addition changed language identity: %d -> %d", before, got)
		}
		check := func(c *sql.Conn, b fixture.Backend) {
			plpgsqlValue(t, p, b, c, "SELECT lanispl AND lanpltrusted AND lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_validator_owner') AS v FROM pg_language WHERE lanname='rl_validator_replay'", "BOOL", true)
			routineLanguagesReplayLinks(t, p, b, c, "rl_validator_replay", "rl_validator_good", true)
			acl := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='rl_validator_replay'", []string{"acl"}, []string{"TEXT"}, [][]any{{"rl_validator_owner=U/rl_validator_owner"}, {"rl_validator_reader=U*/rl_validator_owner"}})
			acl.Bag = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, acl)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(acl))
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','rl_validator_replay','USAGE') AS v", "BOOL", false)
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_validator_reader','rl_validator_replay','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('rl_validator_good','rl_validator_checked') AND proowner=(SELECT oid FROM pg_roles WHERE rolname='rl_validator_reader')", "INT8", int64(2))
			for _, name := range []string{"rl_validator_good", "rl_validator_checked"} {
				parametersValue(t, p, b, c, "SELECT "+name+"($1::integer) AS v", "v", "INT4", int64(5), 4)
				parametersHeld(t, p, b, c, "SELECT "+name+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
			}
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_validator_data", "INT4", int64(5))
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_validator_view", "INT4", int64(6))
			for _, tc := range []struct{ name, signature, args, result, body string }{
				{"rl_validator_bad", "", "", "integer", "BEGIN RETURN 1;"},
				{"rl_validator_result", "", "", "cstring", "BEGIN RETURN NULL; END"},
				{"rl_validator_argument", "cstring", "x cstring", "integer", "BEGIN RETURN NULL; END"},
				{"rl_validator_good", "integer", "x integer", "integer", "BEGIN RETURN x+1; END"},
				{"rl_validator_checked", "integer", "x integer", "integer", "BEGIN RETURN x+1; END"},
			} {
				definition := "CREATE OR REPLACE FUNCTION public." + tc.name + "(" + tc.args + ")\n RETURNS " + tc.result + "\n LANGUAGE rl_validator_replay\nAS $function$" + tc.body + "$function$\n"
				for _, inquiry := range []struct{ name, want string }{
					{"pg_get_functiondef", definition}, {"pg_get_function_arguments", tc.args},
					{"pg_get_function_identity_arguments", tc.args}, {"pg_get_function_result", tc.result},
				} {
					step := fixture.DeparseRelationsTextStep(t, b, "SELECT "+inquiry.name+"('"+tc.name+"("+tc.signature+")'::regprocedure) AS definition", inquiry.want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
				}
				plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AND p.prosrc='"+tc.body+"' AS v FROM pg_proc p JOIN pg_language l ON l.lanname='rl_validator_replay' WHERE p.proname='"+tc.name+"'", "BOOL", true)
			}
			bad := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			bad.InternalPosition, bad.InternalQuery = "16", "BEGIN RETURN 1;"
			bad.Context = "compilation of PL/pgSQL function \"rl_validator_bad\" near line 1"
			routineLanguagesError(t, p, b, c, "SELECT rl_validator_bad()", bad, true, "execute")
			for _, tc := range []struct{ name, query, message string }{
				{"rl_validator_result", "SELECT rl_validator_result()", "PL/pgSQL functions cannot return type cstring"},
				{"rl_validator_argument", "SELECT rl_validator_argument(NULL::cstring)", "PL/pgSQL functions cannot accept type cstring"},
			} {
				d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				d.Context = "compilation of PL/pgSQL function \"" + tc.name + "\" near line 1"
				routineLanguagesError(t, p, b, c, tc.query, d, true, "execute")
			}
			// Restored final routes must still perform ordinary checked admission.
			q := "CREATE FUNCTION rl_validator_rejected() RETURNS integer LANGUAGE rl_validator_replay AS 'BEGIN RETURN 1;'"
			d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			d.Position = strconv.Itoa(strings.Index(q, "BEGIN RETURN 1;") + 16)
			routineLanguagesError(t, p, b, c, q, d, false, "execute")
			routineLanguagesError(t, p, b, c, "CREATE FUNCTION rl_validator_rejected() RETURNS cstring LANGUAGE rl_validator_replay AS 'BEGIN RETURN NULL; END'", fixture.ControlsReferenceDiagnostic("0A000", "PL/pgSQL functions cannot return type cstring"), false, "execute")
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_validator_rejected'", "INT8", int64(0))
			userOperatorExec(t, p, b, c, "DO LANGUAGE rl_validator_replay $$BEGIN END$$")
		}
		check(c, b)
		if !b.PG {
			// This helper captures here, after the actual validator replacement,
			// and retains independent/repeated replay and failed-control checks.
			routineLanguagesExtensionReplay(t, p, b, c, []string{"rl_validator_owner", "rl_validator_reader"}, "rl_validator_good", check)
		}
	})
}
