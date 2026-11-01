package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Keep the backing function reachable: PostgreSQL can prune WHERE FALSE before
// invocation. An ordinary name filter preserves the same independent empty
// expectation without eliminating the function privilege checkpoint.
const pgFileSettingsPrivilegeQuery = pgFileSettingsSelect + " WHERE name='stuntgres_missing_file_parameter'"

func pgFileSettingsPrivilegeRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := fixture.Select(pgFileSettingsPrivilegeQuery, pgFileSettingsColumns, pgFileSettingsTypes, nil)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgFileSettingsMetadata(t, rows, b.PG) }
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func pgFileSettingsDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, function bool) {
	t.Helper()
	message := "permission denied for view pg_file_settings"
	if function {
		message = "permission denied for function pg_show_all_file_settings"
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: message})
		s.SelectRows, s.Prepared = true, prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	}
}

func pgFileSettingsPrivileges(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
	exec := func(e fixture.Executor, query string) {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(query, 0))
	}
	exec(admin, "CREATE ROLE pgfilesettings_actor")
	exec(admin, "CREATE TABLE pgfilesettings_sentinel(n integer)")
	fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO pgfilesettings_sentinel VALUES(42)", 1))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, query := range []string{
			"RESET SESSION AUTHORIZATION",
		} {
			if _, err := actor.ExecContext(ctx, query); err != nil {
				t.Error("actor cleanup", err)
			}
		}
		for _, query := range []string{
			"REVOKE pg_read_all_stats,pg_monitor FROM pgfilesettings_actor",
			"REVOKE SELECT(sourcefile,sourceline,seqno,name,setting,applied,error) ON pg_catalog.pg_file_settings FROM pgfilesettings_actor",
			"REVOKE SELECT ON pg_catalog.pg_file_settings FROM pgfilesettings_actor",
			"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() FROM pgfilesettings_actor",
		} {
			if _, err := admin.ExecContext(ctx, query); err != nil {
				t.Error("ACL cleanup", err)
			}
		}
	}()
	query := pgFileSettingsPrivilegeQuery
	stmt, err := actor.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: query}
	pgFileSettingsInitialACL(t, p, b, admin, true)
	exec(actor, "SET SESSION AUTHORIZATION pgfilesettings_actor")
	ordinary, err := actor.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("ordinary Prepare", err)
	}
	defer ordinary.Close()
	current := fixture.HeldStatement{Executor: actor, Statement: ordinary, SQL: query}
	denied := func(function bool) {
		for _, e := range []fixture.Executor{actor, held, current} {
			pgFileSettingsDenied(t, p, b, e, query, function)
		}
	}
	flags := func(table, function bool) {
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT has_table_privilege('pgfilesettings_actor','pg_catalog.pg_file_settings','SELECT') AS can_select,has_function_privilege('pgfilesettings_actor','pg_catalog.pg_show_all_file_settings()','EXECUTE') AS can_execute", []string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{table, function}}))
	}
	flags(false, false)
	denied(false)
	pgFileSettingsDenied(t, p, b, actor, "SELECT 1 FROM pg_catalog.pg_file_settings", false)
	for _, role := range []string{"pg_read_all_stats", "pg_monitor"} {
		exec(admin, "GRANT "+role+" TO pgfilesettings_actor")
		denied(false)
		exec(admin, "REVOKE "+role+" FROM pgfilesettings_actor")
	}
	exec(admin, "GRANT SELECT ON pg_catalog.pg_file_settings TO pgfilesettings_actor")
	flags(true, false)
	denied(true)
	exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() TO pgfilesettings_actor")
	flags(true, true)
	for _, e := range []fixture.Executor{actor, held, current} {
		pgFileSettingsPrivilegeRead(t, p, b, e)
	}
	pgFileSettingsSource(t, p, b, actor, false)
	pgFileSettingsInitialACL(t, p, b, admin, false)
	exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() FROM pgfilesettings_actor")
	denied(true)
	exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() TO pgfilesettings_actor")
	exec(admin, "REVOKE SELECT ON pg_catalog.pg_file_settings FROM pgfilesettings_actor")
	flags(false, true)
	denied(false)
	exec(admin, "GRANT SELECT(sourcefile) ON pg_catalog.pg_file_settings TO pgfilesettings_actor")
	var mainFile string
	if b.PG {
		if err := admin.QueryRowContext(p.Ctx, "SHOW config_file").Scan(&mainFile); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := actor.QueryContext(p.Ctx, "SELECT sourcefile FROM pg_catalog.pg_file_settings")
	if err != nil {
		t.Fatal(err)
	}
	count, mainSeen := 0, false
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if path == "" {
			rows.Close()
			t.Fatal("empty real file path")
		}
		count++
		mainSeen = mainSeen || path == mainFile
	}
	readErr, closeErr := rows.Err(), rows.Close()
	if readErr != nil || closeErr != nil || !b.PG && count != 0 || b.PG && (count == 0 || !mainSeen) {
		t.Fatal("authorized column-only provenance", count, mainSeen, readErr, closeErr)
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT sourcefile FROM pg_catalog.pg_file_settings WHERE FALSE", []string{"sourcefile"}, []string{"TEXT"}, nil)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
	}
	pgFileSettingsDenied(t, p, b, actor, "SELECT sourcefile FROM pg_catalog.pg_file_settings WHERE name='missing'", false)
	pgFileSettingsDenied(t, p, b, actor, "SELECT sourcefile FROM pg_catalog.pg_file_settings ORDER BY seqno", false)
	denied(false)
	exec(admin, "GRANT SELECT(sourceline,seqno,name,setting,applied,error) ON pg_catalog.pg_file_settings TO pgfilesettings_actor")
	pgFileSettingsPrivilegeRead(t, p, b, held)
	exec(admin, "REVOKE SELECT(sourcefile,sourceline,seqno,name,setting,applied,error) ON pg_catalog.pg_file_settings FROM pgfilesettings_actor")
	denied(false)
	exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_all_file_settings() FROM pgfilesettings_actor")
	flags(false, false)
	exec(actor, "RESET SESSION AUTHORIZATION")
	pgFileSettingsPrivilegeRead(t, p, b, held)
	pgFileSettingsInitialACL(t, p, b, admin, true)
	pgFileSettingsSentinel(t, p, b, admin)
}

func TestReferencePGFileSettingsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgfilesettings_actor"})
	pgFileSettingsScenario(t, pgFileSettingsPrivileges)
}
