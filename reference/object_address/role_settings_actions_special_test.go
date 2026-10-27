package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsSpecialRoleStartupReset(t *testing.T) {
	rsActionCases(t, []string{"rs_initial_login", "rs_initial_target"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		for _, name := range []string{"rs_initial_login", "rs_initial_target"} {
			run(fixture.Exec("CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
		}
		run(fixture.Exec("GRANT rs_initial_target TO rs_initial_login WITH SET TRUE", 0))
		run(fixture.Exec("ALTER ROLE rs_initial_login SET application_name='login_default'", 0))
		run(fixture.Exec("ALTER ROLE rs_initial_target SET application_name='target_default'", 0))
		run(fixture.Exec("ALTER ROLE rs_initial_login SET role FROM CURRENT", 0))
		run(rsActionConfig("rs_initial_login", "{application_name=login_default,role=none}"))
		run(fixture.Exec("ALTER ROLE rs_initial_login SET role='rs_initial_target'", 0))
		pool := fixture.RoleAttributesPool(t, p, b, "rs_initial_login")
		login, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		self := func(s fixture.Step) { rsActionRun(t, p, b, login, route, s) }
		identity := func(current, role string) {
			self(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"rs_initial_login", current}}))
			self(fixture.Select("SHOW role", []string{"role"}, []string{"TEXT"}, [][]any{{role}}))
			self(fixture.Select("SELECT current_setting('role')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{role}}))
			self(rsActionSetting("application_name", "login_default", "login_default", "user"))
		}
		identity("rs_initial_target", "rs_initial_target")
		// role is GUC_NO_SHOW_ALL: it is not a pg_settings row, even when persisted.
		self(fixture.Select("SELECT name,setting,reset_val,source FROM pg_settings WHERE name='role'", []string{"name", "setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, nil))
		self(fixture.Exec("SET ROLE NONE", 0))
		identity("rs_initial_login", "none")
		self(fixture.Exec("RESET ROLE", 0))
		identity("rs_initial_target", "rs_initial_target")
		run(fixture.Exec("ALTER ROLE rs_initial_login RESET role", 0))
		self(fixture.Exec("SET ROLE NONE", 0))
		self(fixture.Exec("RESET ROLE", 0))
		identity("rs_initial_target", "rs_initial_target")
		freshPool := fixture.RoleAttributesPool(t, p, b, "rs_initial_login")
		fresh, err := freshPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		rsActionRun(t, p, b, fresh, route, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"rs_initial_login", "rs_initial_login"}}))
	})
}

func TestReferenceRoleSettingsSpecialRoleInvalidStoredTarget(t *testing.T) {
	rsActionCases(t, []string{"rs_invalid_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		rsActionRun(t, p, b, c, route, fixture.Exec("CREATE ROLE rs_invalid_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		q := "ALTER ROLE rs_invalid_login SET role='rs_nonexistent_initial'"
		noticeBackend := b
		noticeBackend.Recorder = recorder
		rsActionRun(t, p, noticeBackend, noticeConn, route, fixture.Exec(q, 0))
		// PGC_S_TEST deliberately permits this stored value and issues a NOTICE,
		// rather than confusing the issuer's role with the future login's role.
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42704", Message: `role "rs_nonexistent_initial" does not exist`}
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
			t.Fatalf("notices = %#v, want %#v", notices, []fixture.Diagnostic{want})
		}
		rsActionRun(t, p, b, c, route, rsActionConfig("rs_invalid_login", "{role=rs_nonexistent_initial}"))
		var startupNotices []fixture.Diagnostic
		pool := rsStartupPool(t, p, b, "rs_invalid_login", "", &startupNotices)
		login, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		// Pinned18.6 reports failed startup GUC validation as WARNING22023.
		startupWant := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `role "rs_nonexistent_initial" does not exist`}
		if !reflect.DeepEqual(startupNotices, []fixture.Diagnostic{startupWant}) {
			t.Fatalf("startup notices = %#v, want %#v", startupNotices, []fixture.Diagnostic{startupWant})
		}
		rsActionRun(t, p, b, login, route, rsStartupStep(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"rs_invalid_login", "rs_invalid_login"}})))
		rsActionRun(t, p, b, login, route, fixture.Select("SHOW role", []string{"role"}, []string{"TEXT"}, [][]any{{"none"}}))
	})
}

func TestReferenceRoleSettingsSpecialRoleStoredTargetLifecycle(t *testing.T) {
	for _, change := range []string{"ineligible", "dropped", "renamed"} {
		t.Run(change, func(t *testing.T) {
			rsActionCases(t, []string{"rs_start_login", "rs_start_target", "rs_start_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
				run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
				run(fixture.Exec("CREATE ROLE rs_start_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
				run(fixture.Exec("CREATE ROLE rs_start_target", 0))
				run(fixture.Exec("GRANT rs_start_target TO rs_start_login WITH SET TRUE", 0))
				run(fixture.Exec("ALTER ROLE rs_start_login SET application_name='login_default'", 0))
				var assignmentNotices []fixture.Diagnostic
				_, issuer, recorder := fixture.NoticeReferencePool(t, p, b.PG, &assignmentNotices)
				noticeBackend := b
				noticeBackend.Recorder = recorder
				rsActionRun(t, p, noticeBackend, issuer, route, fixture.Exec("ALTER ROLE rs_start_login SET role='rs_start_target'", 0))
				if len(assignmentNotices) != 0 {
					t.Fatalf("assignment notices = %#v", assignmentNotices)
				}
				switch change {
				case "ineligible":
					run(fixture.Exec("GRANT rs_start_target TO rs_start_login WITH SET FALSE", 0))
				case "dropped":
					run(fixture.Exec("DROP ROLE rs_start_target", 0))
				case "renamed":
					run(fixture.Exec("ALTER ROLE rs_start_target RENAME TO rs_start_renamed", 0))
				}
				// Persisted role is text, not a reference rewritten by DROP/RENAME.
				run(rsActionConfig("rs_start_login", "{application_name=login_default,role=rs_start_target}"))
				var notices []fixture.Diagnostic
				pool := rsStartupPool(t, p, b, "rs_start_login", "", &notices)
				login, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer login.Close()
				// Missing text remains stored, but startup emits WARNING22023 and
				// retains the authenticated identity; a denied target uses42501.
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: `role "rs_start_target" does not exist`}
				if change == "ineligible" {
					want.Code = "42501"
					want.Message = `permission denied to set role "rs_start_target"`
				}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatalf("startup notices = %#v, want %#v", notices, []fixture.Diagnostic{want})
				}
				self := func(s fixture.Step) { rsActionRun(t, p, b, login, route, rsStartupStep(s)) }
				self(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"rs_start_login", "rs_start_login"}}))
				self(fixture.Select("SHOW role", []string{"role"}, []string{"TEXT"}, [][]any{{"none"}}))
				self(rsActionSetting("application_name", "login_default", "login_default", "user"))
				self(fixture.Exec("RESET ROLE", 0))
				self(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"rs_start_login"}}))
			})
		})
	}
}

func TestReferenceRoleSettingsTimingClientStartupPriority(t *testing.T) {
	rsActionCases(t, []string{"rs_options_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_options_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
		run(fixture.Exec("ALTER ROLE rs_options_login SET application_name='persisted'", 0))
		baseline := fixture.RoleAttributesPool(t, p, b, "rs_options_login")
		login, err := baseline.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		rsActionRun(t, p, b, login, route, rsActionSetting("application_name", "persisted", "persisted", "user"))
		// Only pq has a public startup-options seam. Native provider precedence
		// belongs to composed-core tests, not a new public Connector API.
		if b.PG {
			var notices []fixture.Diagnostic
			pool := rsStartupPool(t, p, b, "rs_options_login", "-c application_name=client_startup", &notices)
			client, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if len(notices) != 0 {
				t.Fatalf("startup notices = %#v", notices)
			}
			self := func(s fixture.Step) { rsActionRun(t, p, b, client, route, s) }
			self(rsActionSetting("application_name", "client_startup", "client_startup", "client"))
			self(fixture.Exec("SET application_name='session_override'", 0))
			self(rsActionSetting("application_name", "session_override", "client_startup", "session"))
			self(fixture.Exec("RESET application_name", 0))
			self(rsActionSetting("application_name", "client_startup", "client_startup", "client"))
		}
		run(rsActionConfig("rs_options_login", "{application_name=persisted}"))
	})
}

func TestReferenceRoleSettingsActionsFromCurrentRetainedAuthority(t *testing.T) {
	rsActionCases(t, []string{"rs_current_target", "rs_current_actor"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_current_target", 0))
		run(fixture.Exec("CREATE ROLE rs_current_actor", 0))
		run(fixture.Exec("SET application_name='prepare_value'", 0))
		q := "ALTER ROLE rs_current_target SET application_name FROM CURRENT"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE rs_current_actor", 0))
		run(fixture.Exec("SET application_name='denied_execution'", 0))
		denied := fixture.RoleReferenceError(q, "42501", "permission denied to alter role", `Only roles with the CREATEROLE attribute and the ADMIN option on role "rs_current_target" may alter this role.`, false, "execute")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET ROLE", 0))
		run(rsActionConfig("rs_current_target", ""))
		run(fixture.Exec("ALTER ROLE rs_current_actor CREATEROLE", 0))
		run(fixture.Exec("GRANT rs_current_target TO rs_current_actor WITH ADMIN TRUE", 0))
		run(fixture.Exec("SET ROLE rs_current_actor", 0))
		run(fixture.Exec("SET application_name='execution_value'", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET application_name='after_execution'", 0))
		run(rsActionConfig("rs_current_target", "{application_name=execution_value}"))
		run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"after_execution"}}))
	})
}
