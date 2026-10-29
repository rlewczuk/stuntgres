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

func TestReferenceRoutineLanguagesExtensionAliasReplay(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_member_owner", "rl_member_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c,
			"ALTER EXTENSION plpgsql DROP LANGUAGE rl_member",
			"DROP LANGUAGE rl_member CASCADE",
			"DROP OWNED BY rl_member_owner,rl_member_reader CASCADE")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_member_owner", "CREATE ROLE rl_member_reader",
			"CREATE TRUSTED LANGUAGE rl_member"+routineLanguageFull,
			"REVOKE USAGE ON LANGUAGE rl_member FROM PUBLIC",
			"GRANT USAGE ON LANGUAGE rl_member TO rl_member_reader WITH GRANT OPTION",
			"ALTER LANGUAGE rl_member OWNER TO rl_member_owner",
			"ALTER EXTENSION plpgsql ADD LANGUAGE rl_member",
			"CREATE FUNCTION rl_member_fn(x integer) RETURNS integer LANGUAGE rl_member AS $$BEGIN RETURN x+1; END$$")
		check := func(c *sql.Conn, b fixture.Backend) {
			parametersValue(t, p, b, c, "SELECT rl_member_fn($1::integer) AS v", "v", "INT4", int64(5), 4)
			parametersHeld(t, p, b, c, "SELECT rl_member_fn($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
			plpgsqlValue(t, p, b, c, "SELECT lanispl AND lanpltrusted AND lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_member_owner') AS v FROM pg_language WHERE lanname='rl_member'", "BOOL", true)
			acl := fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='rl_member'", []string{"acl"}, []string{"TEXT"}, [][]any{{"rl_member_owner=U/rl_member_owner"}, {"rl_member_reader=U*/rl_member_owner"}})
			acl.Bag = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, acl)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(acl))
			routineLanguagesReplayLinks(t, p, b, c, "rl_member", "rl_member_fn", true)
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','rl_member','USAGE') AS v", "BOOL", false)
			plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_member_reader','rl_member','USAGE WITH GRANT OPTION') AS v", "BOOL", true)
		}
		check(c, b)
		if !b.PG {
			routineLanguagesExtensionReplay(t, p, b, c, []string{"rl_member_owner", "rl_member_reader"}, "rl_member_fn", check)
		}
	})
}

func TestReferenceRoutineLanguagesInstalledDefinitionReplay(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_changed_owner", "rl_changed_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stockOwner := "postgres"
		if b.PG {
			stockOwner = "stuntgres"
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var baseline bool
			q := "SELECT l.lanpltrusted AND l.lanacl IS NULL AND l.lanowner=(SELECT oid FROM pg_roles WHERE rolname='" + stockOwner + "') AND l.lanplcallfoid=h.oid AND l.laninline=i.oid AND l.lanvalidator=v.oid FROM pg_language l JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace WHERE l.lanname='plpgsql'"
			if err := c.QueryRowContext(ctx, q).Scan(&baseline); err != nil || !baseline {
				t.Errorf("installed language cleanup did not restore baseline: %v, %v", baseline, err)
			}
		}()
		// Keep stock owner/NULL ACL unchanged: only trust/routes can make
		// unchangedSeedInstallation recognize this definition delta.
		defer routineLanguagesCleanup(t, c,
			"CREATE OR REPLACE TRUSTED LANGUAGE plpgsql"+routineLanguageFull,
			"DROP OWNED BY rl_changed_owner,rl_changed_reader CASCADE")()
		userOperatorExec(t, p, b, c, "CREATE ROLE rl_changed_owner", "CREATE ROLE rl_changed_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO rl_changed_reader")
		oid := routineLanguagesOID(t, p, c, "plpgsql")
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE LANGUAGE plpgsql"+routineLanguageCall,
			"CREATE FUNCTION rl_changed_fn(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN x+1; END$$",
			"CREATE FUNCTION rl_changed_bad() RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN 1;'")
		if got := routineLanguagesOID(t, p, c, "plpgsql"); got != oid {
			t.Fatalf("replacement changed installed identity: %d -> %d", oid, got)
		}
		check := func(c *sql.Conn, b fixture.Backend) {
			parametersValue(t, p, b, c, "SELECT rl_changed_fn($1::integer) AS v", "v", "INT4", int64(5), 4)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT lanpltrusted,laninline=0 AS no_inline,lanvalidator=0 AS no_validator,lanacl::text,lanowner::regrole::text AS owner FROM pg_language WHERE lanname='plpgsql'", []string{"lanpltrusted", "no_inline", "no_validator", "lanacl", "owner"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{false, true, true, nil, stockOwner}}))
			parametersHeld(t, p, b, c, "SELECT rl_changed_fn($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
			plpgsqlValue(t, p, b, c, "SELECT lanispl AND NOT lanpltrusted AND laninline=0 AND lanvalidator=0 AND lanacl IS NULL AND lanowner=(SELECT oid FROM pg_roles WHERE rolname='"+stockOwner+"') AS v FROM pg_language WHERE lanname='plpgsql'", "BOOL", true)
			routineLanguagesReplayLinks(t, p, b, c, "plpgsql", "rl_changed_fn", false)
			bad := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			bad.InternalPosition, bad.InternalQuery = "16", "BEGIN RETURN 1;"
			bad.Context = "compilation of PL/pgSQL function \"rl_changed_bad\" near line 1"
			routineLanguagesError(t, p, b, c, "SELECT rl_changed_bad()", bad, true, "execute")
			routineLanguagesError(t, p, b, c, "DO LANGUAGE plpgsql $$BEGIN END$$", fixture.ControlsReferenceDiagnostic("0A000", `language "plpgsql" does not support inline code execution`), false, "execute")
			userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION rl_changed_reader")
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for language plpgsql")
			for _, q := range []string{"CREATE FUNCTION rl_changed_denied() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$", "DO LANGUAGE plpgsql $$BEGIN END$$"} {
				routineLanguagesError(t, p, b, c, q, denied, false, "execute")
			}
			// Existing EXECUTE remains usable even though language admission is denied.
			plpgsqlValue(t, p, b, c, "SELECT rl_changed_fn(4) AS v", "INT4", int64(5))
			userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_changed_denied'", "INT8", int64(0))
		}
		check(c, b)
		if !b.PG {
			routineLanguagesExtensionReplay(t, p, b, c, []string{"rl_changed_owner", "rl_changed_reader"}, "rl_changed_fn", check)
		}
	})
}

func routineLanguagesReplayLinks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, language, function string, full bool) {
	t.Helper()
	plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AND l.lanplcallfoid=h.oid AS v FROM pg_proc p JOIN pg_language l ON l.lanname='"+language+"' JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace WHERE p.proname='"+function+"'", "BOOL", true)
	if full {
		plpgsqlValue(t, p, b, c, "SELECT l.laninline=i.oid AND l.lanvalidator=v.oid AS v FROM pg_language l JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace WHERE l.lanname='"+language+"'", "BOOL", true)
	}
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE l.lanname='"+language+"' AND e.extname='plpgsql' AND d.deptype='e'", "INT8", int64(1))
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_proc p ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN pg_language l ON d.refclassid='pg_language'::regclass AND d.refobjid=l.oid WHERE l.lanname='"+language+"' AND p.proname='"+function+"' AND d.deptype='n'", "INT8", int64(1))
	want := int64(1)
	if full {
		want = 3
	}
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid WHERE l.lanname='"+language+"' AND d.refclassid='pg_proc'::regclass AND d.deptype='n'", "INT8", want)
}

// Native controls are explicit extra phases of the same public SQL scenario.
func routineLanguagesExtensionReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, roles []string, function string, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(connector)
	defer destination.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	dc, err := recorded.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	db := fixture.Backend{Recorder: recorder}
	for _, role := range roles {
		userOperatorExec(t, p, db, dc, "CREATE ROLE "+role)
	}
	userOperatorExec(t, p, db, dc, "CREATE TABLE rl_replay_filler(v integer)", "CREATE TRUSTED LANGUAGE rl_replay_filler"+routineLanguageCall)
	for range 2 {
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(dc, db)
		plpgsqlValue(t, p, db, dc, "SELECT count(*) AS v FROM pg_language WHERE lanname='rl_replay_filler'", "INT8", int64(0))
	}
	check(c, b)
	for range 2 {
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
	}
	held, closeHeld := routineLanguagesHeld(t, p, b, c, function)
	defer closeHeld()
	held(4, 5)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+"; CREATE TABLE rl_replay_prefix(v integer); CREATE LANGUAGE rl_replay_invalid HANDLER rl_absent;")); err == nil {
		t.Fatal("late-invalid extension language replay accepted")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled extension language replay", err)
	}
	check(c, b)
	held(9, 10)
	plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='rl_replay_prefix'", "INT8", int64(0))
}
