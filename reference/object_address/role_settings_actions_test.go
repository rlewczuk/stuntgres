package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// rsActionCases supplies the shared fixture and direct/prepared execution routes.
func rsActionCases(t *testing.T, names []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					body(t, p, b, c, route)
				})
			}
		})
	}
}

func rsActionRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string, s fixture.Step) {
	t.Helper()
	if route != "held" || s.ErrorStage == "prepare" {
		fixture.RoleAttributesRun(t, p, b, c, route != "direct", s)
		return
	}
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func rsActionError(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message))
	s.ErrorStage = "execute"
	return s
}

func rsActionSetting(name, value, reset, source string) fixture.Step {
	return fixture.Select("SELECT setting,reset_val,source FROM pg_settings WHERE name='"+name+"'", []string{"setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{value, reset, source}})
}

func rsActionConfig(name, config string) fixture.Step {
	var rows [][]any
	if config != "" {
		rows = [][]any{{config}}
	}
	return fixture.Select("SELECT s.setconfig::text FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname='"+name+"' AND s.setdatabase=0", []string{"setconfig"}, []string{"TEXT"}, rows)
}

func TestReferenceRoleSettingsActionsAliases(t *testing.T) {
	rsActionCases(t, []string{"rs_action_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_action_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
		for _, tc := range []struct{ q, config string }{
			{"ALTER ROLE rs_action_login SET application_name TO 'one'", "{application_name=one}"},
			{"ALTER USER rs_action_login SET application_name = 'two'", "{application_name=two}"},
			{"ALTER ROLE rs_action_login SET APPLICATION_NAME TO 'DEFAULT'", "{application_name=DEFAULT}"},
			{"ALTER USER rs_action_login SET application_name TO DEFAULT", ""},
			{"ALTER ROLE rs_action_login SET search_path TO public, pg_catalog", "{\"search_path=public, pg_catalog\"}"},
			{"ALTER ROLE rs_action_login RESET search_path", ""},
			{"ALTER ROLE rs_action_login SET application_name='three'", "{application_name=three}"},
			{"ALTER USER rs_action_login RESET ALL", ""},
		} {
			run(fixture.Exec(tc.q, 0))
			run(rsActionConfig("rs_action_login", tc.config))
		}
		// Role specifications resolve the effective identity at execution.
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_action_login", 0))
		for _, spec := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			run(fixture.Exec("ALTER ROLE "+spec+" SET application_name='"+strings.ToLower(spec)+"'", 0))
			run(rsActionConfig("rs_action_login", "{application_name="+strings.ToLower(spec)+"}"))
		}
		run(fixture.Exec("ALTER USER CURRENT_USER RESET ALL", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(rsActionConfig("rs_action_login", ""))
	})
}

func TestReferenceRoleSettingsTimingCapturedBaseline(t *testing.T) {
	rsActionCases(t, []string{"rs_timing_login", "rs_timing_peer"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		for _, name := range []string{"rs_timing_login", "rs_timing_peer"} {
			run(fixture.Exec("CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
		}
		run(fixture.Exec("ALTER ROLE rs_timing_login SET application_name='old'", 0))
		run(fixture.Exec("ALTER ROLE rs_timing_peer SET application_name='peer'", 0))
		oldPool := fixture.RoleAttributesPool(t, p, b, "rs_timing_login")
		old, err := oldPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldRun := func(s fixture.Step) { rsActionRun(t, p, b, old, route, s) }
		oldRun(rsActionSetting("application_name", "old", "old", "user"))
		run(fixture.Exec("SET application_name='captured'", 0))
		q := "ALTER USER rs_timing_login SET application_name FROM CURRENT"
		run(fixture.Exec(q, 0))
		run(rsActionConfig("rs_timing_login", "{application_name=captured}"))
		// Hold before the final issuer change: capture belongs to Execute, not Prepare.
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("SET application_name='execution'", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
		run(rsActionConfig("rs_timing_login", "{application_name=execution}"))
		oldRun(rsActionSetting("application_name", "old", "old", "user"))
		run(fixture.Exec("SET application_name='later'", 0))
		freshPool := fixture.RoleAttributesPool(t, p, b, "rs_timing_login")
		fresh, err := freshPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		freshRun := func(s fixture.Step) { rsActionRun(t, p, b, fresh, route, s) }
		freshRun(rsActionSetting("application_name", "execution", "execution", "user"))
		run(fixture.Exec("ALTER ROLE rs_timing_login RESET application_name", 0))
		for _, s := range []fixture.Step{fixture.Exec("SET application_name='override'", 0), fixture.Exec("RESET application_name", 0), rsActionSetting("application_name", "execution", "execution", "user"), fixture.Exec("SET application_name=DEFAULT", 0), rsActionSetting("application_name", "execution", "execution", "user")} {
			freshRun(s)
		}
		// Neither identity switch reinitializes defaults; both are on a privileged physical session.
		run(fixture.Exec("SET ROLE rs_timing_peer", 0))
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"later"}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_timing_peer", 0))
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"later"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		latestPool := fixture.RoleAttributesPool(t, p, b, "rs_timing_login")
		latest, err := latestPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer latest.Close()
		rsActionRun(t, p, b, latest, route, rsActionSetting("application_name", "", "", "default"))
		// Returning and reborrowing the sole physical owner does not reload defaults.
		if err := fresh.Close(); err != nil {
			t.Fatal(err)
		}
		reused, err := freshPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reused.Close()
		rsActionRun(t, p, b, reused, route, rsActionSetting("application_name", "execution", "execution", "user"))
	})
}

func TestReferenceRoleSettingsActionsCatalogProjections(t *testing.T) {
	rsActionCases(t, []string{"rs_projection_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_projection_login LOGIN", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER ROLE rs_projection_login IN DATABASE \""+strings.ReplaceAll(database, "\"", "\"\"")+"\" SET application_name='database_projection'", 0))
		for _, config := range []string{"{application_name=projection}", ""} {
			if config != "" {
				run(fixture.Exec("ALTER ROLE rs_projection_login SET application_name='projection'", 0))
			} else {
				run(fixture.Exec("ALTER ROLE rs_projection_login RESET ALL", 0))
			}
			run(rsActionConfig("rs_projection_login", config))
			// This row survives role-only RESET ALL, but never feeds role-only views.
			run(fixture.Select("SELECT d.datname,r.rolname,s.setconfig::text FROM pg_db_role_setting s JOIN pg_database d ON d.oid=s.setdatabase JOIN pg_roles r ON r.oid=s.setrole WHERE d.datname=current_database() AND r.rolname='rs_projection_login'", []string{"datname", "rolname", "setconfig"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{database, "rs_projection_login", "{application_name=database_projection}"}}))
			var value any
			if config != "" {
				value = config
			}
			run(fixture.Select("SELECT rolconfig::text FROM pg_roles WHERE rolname='rs_projection_login'", []string{"rolconfig"}, []string{"TEXT"}, [][]any{{value}}))
			run(fixture.Select("SELECT useconfig::text FROM pg_user WHERE usename='rs_projection_login'", []string{"useconfig"}, []string{"TEXT"}, [][]any{{value}}))
			shadow := fixture.Select("SELECT useconfig FROM pg_shadow WHERE usename='rs_projection_login'", []string{"useconfig"}, []string{"_TEXT"}, [][]any{{value}})
			// Keep a real privileged preparation across the visibility change.
			var held *sql.Stmt
			if route == "held" {
				var err error
				held, err = c.PrepareContext(p.Ctx, shadow.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: shadow.Query}, b.Recorder, shadow)
			} else {
				run(shadow)
			}
			run(fixture.Exec("SET ROLE rs_projection_login", 0))
			// Public projections remain visible with the same populated/NULL image.
			run(fixture.Select("SELECT useconfig::text FROM pg_user WHERE usename='rs_projection_login'", []string{"useconfig"}, []string{"TEXT"}, [][]any{{value}}))
			denied := rsActionError(shadow.Query, "42501", "permission denied for view pg_shadow")
			denied.SelectRows = true
			if held != nil {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: shadow.Query}, b.Recorder, denied)
			} else {
				run(denied)
			}
			run(fixture.Exec("RESET ROLE", 0))
			if held != nil {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: shadow.Query}, b.Recorder, shadow)
			} else {
				run(shadow)
			}
		}
	})
}
