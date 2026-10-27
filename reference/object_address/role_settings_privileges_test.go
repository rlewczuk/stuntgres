package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsPrivilegesAdministration(t *testing.T) {
	rsActionCases(t, []string{"rs_priv_actor", "rs_priv_target", "rs_priv_super"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		for _, q := range []string{"CREATE ROLE rs_priv_actor", "CREATE ROLE rs_priv_target", "CREATE ROLE rs_priv_super SUPERUSER"} {
			run(fixture.Exec(q, 0))
		}
		denied := func(q, detail string) {
			s := fixture.RoleReferenceError(q, "42501", "permission denied to alter role", detail, false, "execute")
			run(s)
		}
		run(fixture.Exec("SET ROLE rs_priv_actor", 0))
		run(fixture.Exec("ALTER ROLE rs_priv_actor SET application_name='self'", 0))
		q := "ALTER ROLE rs_priv_target SET application_name='target'"
		detail := `Only roles with the CREATEROLE attribute and the ADMIN option on role "rs_priv_target" may alter this role.`
		denied(q, detail)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER ROLE rs_priv_actor CREATEROLE", 0))
		run(fixture.Exec("SET ROLE rs_priv_actor", 0))
		denied(q, detail)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER ROLE rs_priv_actor NOCREATEROLE", 0))
		run(fixture.Exec("GRANT rs_priv_target TO rs_priv_actor WITH ADMIN TRUE", 0))
		run(fixture.Exec("SET ROLE rs_priv_actor", 0))
		denied(q, detail)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER ROLE rs_priv_actor CREATEROLE", 0))
		run(fixture.Exec("SET ROLE rs_priv_actor", 0))
		run(fixture.Exec(q, 0))
		denied("ALTER ROLE rs_priv_super SET application_name='forbidden'", `Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.`)
		s := fixture.RoleReferenceError("ALTER ROLE ALL SET application_name='forbidden'", "42501", "permission denied to alter setting", "Only roles with the SUPERUSER attribute may alter settings globally.", false, "execute")
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
		run(rsActionConfig("rs_priv_target", "{application_name=target}"))
		run(rsActionConfig("rs_priv_super", ""))
	})
}

func TestReferenceRoleSettingsPrivilegesParameterGrants(t *testing.T) {
	rsActionCases(t, []string{"rs_parameter_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
		run := func(s fixture.Step) { rsActionRun(t, p, b, c, route, s) }
		run(fixture.Exec("CREATE ROLE rs_parameter_login LOGIN PASSWORD 'role-attributes-fixture'", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.ExecContext(ctx, "RESET ROLE")
			if _, err := c.ExecContext(ctx, "REVOKE SET ON PARAMETER log_statement_stats, rs_custom.value FROM rs_parameter_login"); err != nil {
				t.Error(err)
			}
		}()
		loginPool := fixture.RoleAttributesPool(t, p, b, "rs_parameter_login")
		login, err := loginPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		self := func(s fixture.Step) { rsActionRun(t, p, b, login, route, s) }
		protected := "ALTER ROLE rs_parameter_login SET log_statement_stats=off"
		custom := "ALTER ROLE rs_parameter_login SET rs_custom.value='custom'"
		for _, q := range []string{protected, custom} {
			name := "log_statement_stats"
			if q == custom {
				name = "rs_custom.value"
			}
			self(rsActionError(q, "42501", `permission denied to set parameter "`+name+`"`))
		}
		// Live custom SET is allowed; persisting the identical placeholder is not.
		self(fixture.Exec("SET rs_custom.value='live'", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER log_statement_stats, rs_custom.value TO rs_parameter_login", 0))
		self(fixture.Exec(protected, 0))
		self(fixture.Exec(custom, 0))
		self(fixture.Exec("ALTER ROLE rs_parameter_login SET application_name='ordinary'", 0))
		// This held utility must use the authority at Execute after REVOKE.
		held, err := login.PrepareContext(p.Ctx, protected)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(fixture.Exec("REVOKE SET ON PARAMETER log_statement_stats, rs_custom.value FROM rs_parameter_login", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: login, Statement: held, SQL: protected}, b.Recorder, rsActionError(protected, "42501", `permission denied to set parameter "log_statement_stats"`))
		for _, tail := range []string{"RESET log_statement_stats", "SET log_statement_stats=DEFAULT", "SET log_statement_stats FROM CURRENT"} {
			self(rsActionError("ALTER ROLE rs_parameter_login "+tail, "42501", `permission denied to set parameter "log_statement_stats"`))
		}
		self(fixture.Exec("ALTER ROLE rs_parameter_login RESET ALL", 0))
		run(rsActionConfig("rs_parameter_login", "{log_statement_stats=off,rs_custom.value=custom}"))
		freshPool := fixture.RoleAttributesPool(t, p, b, "rs_parameter_login")
		fresh, err := freshPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		rsActionRun(t, p, b, fresh, route, fixture.Select("SELECT current_setting('rs_custom.value')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"custom"}}))
		// A monitor role makes protected pg_settings columns observable without SET authority.
		run(fixture.Exec("GRANT pg_read_all_settings TO rs_parameter_login", 0))
		rsActionRun(t, p, b, fresh, route, rsActionSetting("log_statement_stats", "off", "off", "user"))
		run(fixture.Exec("ALTER ROLE rs_parameter_login RESET ALL", 0))
		run(rsActionConfig("rs_parameter_login", ""))
	})
}
