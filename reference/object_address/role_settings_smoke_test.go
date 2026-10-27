package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsFeatureSmoke(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		run := func(s fixture.Step) { rsDatabaseRun(t, p, b, admin, route, s) }
		for _, q := range []string{
			"ALTER ROLE ALL SET application_name='G'",
			"ALTER ROLE ALL IN DATABASE rsd_one SET application_name='D'",
			"ALTER ROLE rsd_a SET application_name='R'",
			"SET application_name='captured'",
			"ALTER USER rsd_a IN DATABASE rsd_one SET application_name FROM CURRENT",
			"ALTER ROLE rsd_a SET lc_messages='C'",
		} {
			run(fixture.Exec(q, 0))
		}
		run(rsDatabaseApplication("captured"))
		old, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_a")
		old.DB.SetMaxOpenConns(1)
		rsDatabaseRun(t, p, old, old.DB, route, rsDatabaseApplication("captured"))
		run(fixture.Exec("ALTER USER rsd_a RENAME TO rsd_renamed", 0))
		run(fixture.Exec("SET application_name='later'", 0))
		fresh, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_renamed")
		c, err := fresh.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		user := func(s fixture.Step) { rsDatabaseRun(t, p, fresh, c, route, s) }
		user(fixture.Select("SELECT session_user,current_user,current_setting('application_name') AS value", []string{"session_user", "current_user", "value"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"rsd_renamed", "rsd_renamed", "captured"}}))
		user(fixture.Exec("SET application_name='override'", 0))
		user(fixture.Exec("RESET application_name", 0))
		user(rsDatabaseApplication("captured"))
		run(fixture.Exec("GRANT rsd_b TO rsd_renamed", 0))
		user(fixture.Exec("SET ROLE rsd_b", 0))
		user(rsDatabaseApplication("captured"))
		user(fixture.Exec("RESET ROLE", 0))
		for _, failure := range []struct{ query, code, message, detail string }{
			{"ALTER ROLE rsd_renamed RENAME TO rsd_a", "0A000", "session user cannot be renamed", ""},
			{"ALTER ROLE rsd_missing SET application_name='x'", "42704", "role \"rsd_missing\" does not exist", ""},
			{"ALTER ROLE rsd_b SET application_name='x'", "42501", "permission denied to alter role", "Only roles with the CREATEROLE attribute and the ADMIN option on role \"rsd_b\" may alter this role."},
		} {
			user(fixture.RoleReferenceError(failure.query, failure.code, failure.message, failure.detail, false, "execute"))
		}
		// Ordinary RESET ALL cannot clear an administrator's protected lc_messages.
		user(fixture.Exec("ALTER ROLE rsd_renamed RESET ALL", 0))
		protected, _ := rsDatabasePool(t, p, b, "rsd_two", "rsd_renamed")
		rsDatabaseRun(t, p, protected, protected.DB, route, fixture.Select("SELECT name,setting,reset_val,source FROM pg_settings WHERE name='lc_messages'", []string{"name", "setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"lc_messages", "C", "C", "user"}}))
		rsDatabaseRun(t, p, protected, protected.DB, route, rsDatabaseApplication("G"))
		// Per-database row survives resetting the distinct role-only row.
		after, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_renamed")
		rsDatabaseRun(t, p, after, after.DB, route, rsDatabaseApplication("captured"))
		run(fixture.Exec("ALTER ROLE rsd_renamed IN DATABASE rsd_one RESET ALL", 0))
		ladder, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_renamed")
		rsDatabaseRun(t, p, ladder, ladder.DB, route, rsDatabaseApplication("D"))
		run(fixture.Exec("ALTER ROLE ALL IN DATABASE rsd_one RESET ALL", 0))
		global, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_renamed")
		rsDatabaseRun(t, p, global, global.DB, route, rsDatabaseApplication("G"))
		// Already initialized physical sessions still reset to their captured value.
		user(fixture.Exec("SET application_name='temporary'", 0))
		user(fixture.Exec("RESET application_name", 0))
		user(rsDatabaseApplication("captured"))
		owner, _ := rsDatabasePool(t, p, b, "rsd_two", "rsd_owner")
		rsDatabaseRun(t, p, owner, owner.DB, route, fixture.Exec("ALTER ROLE ALL IN DATABASE rsd_one SET application_name='owner'", 0))
		user(fixture.RoleReferenceError("ALTER ROLE ALL IN DATABASE rsd_one RESET ALL", "42501", "must be owner of database rsd_one", "", false, "execute"))
	})
}
