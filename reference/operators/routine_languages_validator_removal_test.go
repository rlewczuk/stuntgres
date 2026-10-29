package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesValidatorRemoval(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_removal_owner", "rl_removal_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			routineLanguagesCleanup(t, c)()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var members int64
			q := "SELECT count(*) FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='rl_removed_validator' AND e.extname='plpgsql' AND d.deptype='e'"
			if err := c.QueryRowContext(ctx, q).Scan(&members); err != nil {
				t.Error(err)
			} else if members != 0 {
				routineLanguagesCleanup(t, c, "ALTER EXTENSION plpgsql DROP LANGUAGE rl_removed_validator")()
			}
			routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_removed_validator CASCADE")()
			for _, role := range []string{"rl_removal_owner", "rl_removal_reader"} {
				var exists bool
				if err := c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
					t.Error(err)
				} else if exists {
					routineLanguagesCleanup(t, c, "DROP OWNED BY "+role+" CASCADE")()
				}
			}
		}()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_removal_owner", "CREATE ROLE rl_removal_reader",
			"CREATE TRUSTED LANGUAGE rl_removed_validator"+routineLanguageFull,
			"CREATE FUNCTION rl_removal_checked(x integer) RETURNS integer LANGUAGE rl_removed_validator AS 'BEGIN RETURN x+2; END'")
		original := routineLanguagesOID(t, p, c, "rl_removed_validator")
		userOperatorExec(t, p, b, c,
			"CREATE OR REPLACE TRUSTED LANGUAGE rl_removed_validator"+routineLanguageCall,
			"CREATE FUNCTION rl_removal_good(x integer) RETURNS integer LANGUAGE rl_removed_validator AS 'BEGIN RETURN x+1; END'",
			"CREATE FUNCTION rl_removal_bad() RETURNS integer LANGUAGE rl_removed_validator AS 'BEGIN RETURN 1;'",
			"CREATE FUNCTION rl_removal_result() RETURNS cstring LANGUAGE rl_removed_validator AS 'BEGIN RETURN NULL; END'",
			"REVOKE USAGE ON LANGUAGE rl_removed_validator FROM PUBLIC",
			"GRANT USAGE ON LANGUAGE rl_removed_validator TO rl_removal_reader WITH GRANT OPTION",
			"ALTER LANGUAGE rl_removed_validator OWNER TO rl_removal_owner",
			"ALTER EXTENSION plpgsql ADD LANGUAGE rl_removed_validator",
			"GRANT USAGE,CREATE ON SCHEMA public TO rl_removal_reader",
			"ALTER FUNCTION rl_removal_checked(integer) OWNER TO rl_removal_reader",
			"ALTER FUNCTION rl_removal_good(integer) OWNER TO rl_removal_reader",
			"CREATE TABLE rl_removal_data(id integer,v integer DEFAULT rl_removal_good(4) CONSTRAINT rl_removal_positive CHECK (rl_removal_good(v)>0))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_removal_data(id) VALUES(1)", 1))
		userOperatorExec(t, p, b, c, "CREATE VIEW rl_removal_view AS SELECT rl_removal_good(v) AS v FROM rl_removal_data")
		if got := routineLanguagesOID(t, p, c, "rl_removed_validator"); got != original {
			t.Fatalf("validator removal changed language identity: %d -> %d", original, got)
		}
		check := func(c *sql.Conn, b fixture.Backend) {
			plpgsqlValue(t, p, b, c, "SELECT lanispl AND lanpltrusted AND laninline=0 AND lanvalidator=0 AND lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_removal_owner') AS v FROM pg_language WHERE lanname='rl_removed_validator'", "BOOL", true)
			routineLanguagesReplayLinks(t, p, b, c, "rl_removed_validator", "rl_removal_checked", false)
			acl := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='rl_removed_validator'", []string{"acl"}, []string{"TEXT"}, [][]any{{"rl_removal_owner=U/rl_removal_owner"}, {"rl_removal_reader=U*/rl_removal_owner"}})
			acl.Bag = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, acl)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(acl))
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','rl_removed_validator','USAGE') AS v", "BOOL", false)
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_removal_reader','rl_removed_validator','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname IN ('rl_removal_checked','rl_removal_good') AND proowner=(SELECT oid FROM pg_roles WHERE rolname='rl_removal_reader')", "INT8", int64(2))
			for _, tc := range []struct {
				name          string
				first, second int64
			}{{"rl_removal_checked", 6, 11}, {"rl_removal_good", 5, 10}} {
				parametersValue(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "v", "INT4", tc.first, 4)
				parametersHeld(t, p, b, c, "SELECT "+tc.name+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{tc.first, tc.second})
			}
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_removal_data WHERE id=1", "INT4", int64(5))
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_removal_view", "INT4", int64(6))
			for _, tc := range []struct{ name, signature, args, result, body string }{
				{"rl_removal_checked", "integer", "x integer", "integer", "BEGIN RETURN x+2; END"},
				{"rl_removal_good", "integer", "x integer", "integer", "BEGIN RETURN x+1; END"},
				{"rl_removal_bad", "", "", "integer", "BEGIN RETURN 1;"},
				{"rl_removal_result", "", "", "cstring", "BEGIN RETURN NULL; END"},
			} {
				definition := "CREATE OR REPLACE FUNCTION public." + tc.name + "(" + tc.args + ")\n RETURNS " + tc.result + "\n LANGUAGE rl_removed_validator\nAS $function$" + tc.body + "$function$\n"
				for _, inquiry := range []struct{ name, want string }{{"pg_get_functiondef", definition}, {"pg_get_function_arguments", tc.args}, {"pg_get_function_identity_arguments", tc.args}, {"pg_get_function_result", tc.result}} {
					step := fixture.DeparseRelationsTextStep(t, b, "SELECT "+inquiry.name+"('"+tc.name+"("+tc.signature+")'::regprocedure) AS definition", inquiry.want)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
				}
				plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AND p.prosrc='"+tc.body+"' AS v FROM pg_proc p JOIN pg_language l ON l.lanname='rl_removed_validator' WHERE p.proname='"+tc.name+"'", "BOOL", true)
			}
			bad := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			bad.InternalPosition, bad.InternalQuery = "16", "BEGIN RETURN 1;"
			bad.Context = "compilation of PL/pgSQL function \"rl_removal_bad\" near line 1"
			routineLanguagesError(t, p, b, c, "SELECT rl_removal_bad()", bad, true, "execute")
			result := fixture.ControlsReferenceDiagnostic("0A000", "PL/pgSQL functions cannot return type cstring")
			result.Context = "compilation of PL/pgSQL function \"rl_removal_result\" near line 1"
			routineLanguagesError(t, p, b, c, "SELECT rl_removal_result()", result, true, "execute")
			routineLanguagesError(t, p, b, c, "DO LANGUAGE rl_removed_validator $$BEGIN END$$", fixture.ControlsReferenceDiagnostic("0A000", `language "rl_removed_validator" does not support inline code execution`), false, "execute")
		}
		check(c, b)
		if !b.PG {
			// Capture AFTER removal. Corrupt the checked body only, not its
			// source-only siblings: failed Restore must retain public consumers.
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if strings.Count(saved.String(), "BEGIN RETURN x+2; END") != 1 {
				t.Fatal("checked corruption source is not unique")
			}
			held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_removal_checked")
			defer closeHeld()
			held(4, 6)
			corrupt := strings.Replace(saved.String(), "BEGIN RETURN x+2; END", "BEGIN RETURN 1;", 1)
			var diagnostic *stuntgres.Error
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(corrupt)); !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" {
				t.Fatal("validator removal silently suppressed checked-body Restore failure", err)
			}
			check(c, b)
			held(9, 11)
			routineLanguagesExtensionReplay(t, p, b, c, []string{"rl_removal_owner", "rl_removal_reader"}, "rl_removal_good", check)
		}
	})
}
