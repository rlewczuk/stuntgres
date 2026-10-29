package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_reader", "rl_owner", "rl_stranger"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_trusted,rl_untrusted,rl_forbidden CASCADE", "DROP OWNED BY rl_reader,rl_owner,rl_stranger CASCADE")()
		userOperatorExec(t, p, b, c,
			"CREATE ROLE rl_reader", "CREATE ROLE rl_owner", "CREATE ROLE rl_stranger",
			"GRANT USAGE,CREATE ON SCHEMA public TO rl_reader,rl_owner,rl_stranger",
			"CREATE TRUSTED LANGUAGE rl_trusted"+routineLanguageFull,
			"CREATE LANGUAGE rl_untrusted"+routineLanguageFull,
			"CREATE FUNCTION rl_allowed(x integer) RETURNS integer LANGUAGE rl_trusted AS $$BEGIN RETURN x+1; END$$")
		plpgsqlValue(t, p, b, c, "SELECT lanacl IS NULL AS v FROM pg_language WHERE lanname='rl_trusted'", "BOOL", true)
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_reader",
			"CREATE FUNCTION rl_reader_fn() RETURNS integer LANGUAGE rl_trusted AS $$BEGIN RETURN 5; END$$",
			"DO LANGUAGE rl_trusted $$BEGIN PERFORM 1; END$$")
		held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_allowed")
		defer closeHeld()
		q := "SELECT rl_allowed($1::integer) AS v"
		deniedHeld, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer deniedHeld.Close()
		held(4, 5)
		routineLanguagesError(t, p, b, c, "CREATE LANGUAGE rl_forbidden HANDLER rl_absent", fixture.ControlsReferenceDiagnostic("42501", "must be superuser to create custom procedural language"), false, "execute")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE USAGE ON LANGUAGE rl_trusted FROM PUBLIC,rl_reader", "SET SESSION AUTHORIZATION rl_reader")
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for language rl_trusted")
		for _, q := range []string{"CREATE FUNCTION rl_denied() RETURNS integer LANGUAGE rl_trusted AS $$BEGIN RETURN 1; END$$", "DO LANGUAGE rl_trusted $$BEGIN END$$"} {
			routineLanguagesError(t, p, b, c, q, denied, false, "execute")
		}
		held(9, 10)
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION rl_allowed(integer) FROM PUBLIC,rl_reader", "SET SESSION AUTHORIZATION rl_reader")
		// This is the original retained handle, not a newly prepared call.
		rows, err := deniedHeld.QueryContext(p.Ctx, 4)
		if rows != nil {
			rows.Close()
			t.Fatal("denied held call published rows")
		}
		wantExecute := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function rl_allowed")
		fixture.AssertDiagnostic(t, err, &wantExecute)
		routineLanguagesError(t, p, b, c, "SELECT rl_allowed(4)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function rl_allowed"), true, "execute")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION rl_allowed(integer) TO rl_reader")
		untrustedGrant := fixture.ControlsReferenceDiagnostic("42809", `language "rl_untrusted" is not trusted`)
		untrustedGrant.Detail = "GRANT and REVOKE are not allowed on untrusted languages, because only superusers can use untrusted languages."
		routineLanguagesError(t, p, b, c, "GRANT USAGE ON LANGUAGE rl_untrusted TO rl_reader", untrustedGrant, false, "execute")
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_reader")
		held(4, 5)
		routineLanguagesError(t, p, b, c, "CREATE FUNCTION rl_untrusted_fn() RETURNS integer LANGUAGE rl_untrusted AS $$BEGIN RETURN 1; END$$", fixture.ControlsReferenceDiagnostic("42501", "permission denied for language rl_untrusted"), false, "execute")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT USAGE ON LANGUAGE rl_trusted TO rl_reader WITH GRANT OPTION", "ALTER LANGUAGE rl_trusted OWNER TO rl_owner")
		plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_owner') AS v FROM pg_language WHERE lanname='rl_trusted'", "BOOL", true)
		aclStep := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='rl_trusted'", []string{"acl"}, []string{"TEXT"}, [][]any{{"rl_owner=U/rl_owner"}, {"rl_reader=U*/rl_owner"}})
		aclStep.Bag = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, aclStep)
		plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_reader','rl_trusted','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_owner")
		routineLanguagesError(t, p, b, c, "ALTER LANGUAGE rl_trusted OWNER TO rl_stranger", fixture.ControlsReferenceDiagnostic("42501", `must be able to SET ROLE "rl_stranger"`), false, "execute")
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT rl_stranger TO rl_owner", "SET SESSION AUTHORIZATION rl_owner", "ALTER LANGUAGE rl_trusted OWNER TO rl_stranger", "RESET SESSION AUTHORIZATION")
		plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_stranger') AS v FROM pg_language WHERE lanname='rl_trusted'", "BOOL", true)
		// Explicit empty ACL must not turn back into trusted PUBLIC defaults.
		userOperatorExec(t, p, b, c, "REVOKE ALL ON LANGUAGE rl_trusted FROM PUBLIC,rl_reader,rl_stranger CASCADE")
		plpgsqlValue(t, p, b, c, "SELECT lanacl IS NOT NULL AND cardinality(lanacl)=0 AS v FROM pg_language WHERE lanname='rl_trusted'", "BOOL", true)
		plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_reader','rl_trusted','USAGE') AS v", "BOOL", false)
	})
}

func TestReferenceRoutineLanguagesIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_identity_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c,
			"DROP LANGUAGE IF EXISTS rl_identity,rl_renamed,rl_collision CASCADE",
			"DROP OWNED BY rl_identity_reader CASCADE")()
		defer routineLanguagesRestoreBuiltin(t, c, "rl_sql_renamed", "sql")()
		defer routineLanguagesRestoreBuiltin(t, c, "rl_pl_renamed", "plpgsql")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_identity_reader", "CREATE TRUSTED LANGUAGE rl_identity"+routineLanguageFull,
			"CREATE TRUSTED LANGUAGE rl_collision"+routineLanguageCall,
			"GRANT USAGE ON LANGUAGE rl_identity TO rl_identity_reader",
			"CREATE FUNCTION rl_identity_fn(x integer) RETURNS integer LANGUAGE rl_identity AS $$BEGIN RETURN x+1; END$$",
			"CREATE FUNCTION rl_sql_fn(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rl_pl_fn(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN x+1; END$$",
			"CREATE VIEW rl_identity_view AS SELECT rl_identity_fn(4) AS v")
		oid := routineLanguagesOID(t, p, c, "rl_identity")
		var owner, acl string
		if err := c.QueryRowContext(p.Ctx, "SELECT lanowner::text,lanacl::text FROM pg_language WHERE lanname='rl_identity'").Scan(&owner, &acl); err != nil {
			t.Fatal(err)
		}
		held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_identity_fn")
		defer closeHeld()
		sqlHeld, closeSQL := routineLanguagesHeld(t, p, b, c, "rl_sql_fn")
		defer closeSQL()
		plHeld, closePL := routineLanguagesHeld(t, p, b, c, "rl_pl_fn")
		defer closePL()
		held(4, 5)
		userOperatorExec(t, p, b, c, "ALTER LANGUAGE rl_identity RENAME TO rl_renamed", "ALTER LANGUAGE sql RENAME TO rl_sql_renamed", "ALTER LANGUAGE plpgsql RENAME TO rl_pl_renamed")
		if got := routineLanguagesOID(t, p, c, "rl_renamed"); got != oid {
			t.Fatalf("rename changed OID: %d -> %d", oid, got)
		}
		for _, check := range []func(int, int64){held, sqlHeld, plHeld} {
			check(9, 10)
		}
		plpgsqlValue(t, p, b, c, "SELECT v FROM rl_identity_view", "INT4", int64(5))
		for _, name := range []string{"rl_identity", "sql", "plpgsql"} {
			q := "CREATE FUNCTION rl_old_name() RETURNS integer LANGUAGE " + name + " AS 'SELECT 5'"
			if name == "plpgsql" {
				d := fixture.ControlsReferenceDiagnostic("42704", `language "plpgsql" does not exist`)
				d.Hint = "Use CREATE EXTENSION to load the language into the database."
				routineLanguagesError(t, p, b, c, q, d, false, "execute")
			} else {
				routineLanguagesMissing(t, p, b, c, q, name)
			}
		}
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION rl_new_sql(x integer) RETURNS integer LANGUAGE rl_sql_renamed AS 'SELECT $1+1'",
			"CREATE FUNCTION rl_new_pl(x integer) RETURNS integer LANGUAGE rl_pl_renamed AS $$BEGIN RETURN x+1; END$$",
			"CREATE OR REPLACE TRUSTED LANGUAGE rl_renamed"+routineLanguageCall)
		for _, fn := range []string{"rl_new_sql", "rl_new_pl"} {
			plpgsqlValue(t, p, b, c, "SELECT "+fn+"(4) AS v", "INT4", int64(5))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid::bigint,lanowner::text,lanacl::text,laninline=0,lanvalidator=0 FROM pg_language WHERE lanname='rl_renamed'", []string{"oid", "lanowner", "lanacl", "?column?", "?column?"}, []string{"INT8", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{oid, owner, acl, true, true}}))
		routineLanguagesError(t, p, b, c, "ALTER LANGUAGE rl_renamed RENAME TO rl_collision", fixture.ControlsReferenceDiagnostic("42710", `language "rl_collision" already exists`), false, "execute")
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_identity_reader")
		for _, q := range []string{"ALTER LANGUAGE rl_renamed RENAME TO rl_stolen", "ALTER LANGUAGE rl_renamed OWNER TO rl_identity_reader", "DROP LANGUAGE rl_renamed CASCADE"} {
			routineLanguagesError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of language rl_renamed"), false, "execute")
		}
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		held(4, 5)
		if got := routineLanguagesOID(t, p, c, "rl_renamed"); got != oid {
			t.Fatalf("failed ALTER/DROP changed OID: %d -> %d", oid, got)
		}
	})
}
