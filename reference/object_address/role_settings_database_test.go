package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// All selectors use real selected databases and one shared cluster role authority.
func rsDatabaseCases(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, string)) {
	t.Helper()
	for _, route := range []string{"direct", "fresh", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rsd_a", "rsd_b", "rsd_owner", "rsd_renamed"})
			dbs := []string{"rsd_one", "rsd_two"}
			fixture.DatabaseReferenceCleanup(t, &dbs)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if _, err := b.DB.ExecContext(ctx, "ALTER ROLE ALL RESET ALL"); err != nil {
							t.Error(err)
						}
					})
					for _, q := range []string{
						"CREATE ROLE rsd_a LOGIN PASSWORD 'role-core-fixture'",
						"CREATE ROLE rsd_b LOGIN PASSWORD 'role-core-fixture'",
						"CREATE ROLE rsd_owner LOGIN PASSWORD 'role-core-fixture'",
						"CREATE DATABASE rsd_one OWNER rsd_owner", "CREATE DATABASE rsd_two",
					} {
						fixture.RoleAttributesRun(t, p, b, b.DB, false, fixture.Exec(q, 0))
					}
					body(t, p, b, route)
				})
			}
		})
	}
}

func rsDatabaseRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, route string, s fixture.Step) {
	t.Helper()
	if route == "held" {
		fixture.RoleTransactionStep(t, p, b, e, true, s)
		return
	}
	fixture.RoleAttributesRun(t, p, b, e, route == "fresh", s)
}

func rsDatabasePool(t *testing.T, p *fixture.Pair, b fixture.Backend, database, role string) (fixture.Backend, *sql.DB) {
	t.Helper()
	db, raw, rec := fixture.DatabaseScenarioPool(t, p, b.PG, database, role)
	b.DB, b.Recorder = db, rec
	return b, raw
}

func rsDatabaseApplication(value string) fixture.Step {
	return fixture.Select("SELECT current_setting('application_name') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{value}})
}

func rsDatabaseFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, route, database, role, application, timeout, source string) {
	t.Helper()
	fresh, _ := rsDatabasePool(t, p, b, database, role)
	defer fresh.DB.Close()
	rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(application))
	rsDatabaseRun(t, p, fresh, fresh.DB, route, fixture.Select(
		"SELECT name,setting,reset_val,source FROM pg_settings WHERE name IN ('application_name','statement_timeout') ORDER BY name",
		[]string{"name", "setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{"application_name", application, application, source}, {"statement_timeout", timeout, timeout, "global"}}))
}

func TestReferenceRoleSettingsDatabasePrecedence(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		run := func(s fixture.Step) { rsDatabaseRun(t, p, b, b.DB, route, s) }
		for _, q := range []string{
			"ALTER ROLE ALL SET application_name='G'", "ALTER ROLE ALL SET statement_timeout='9s'",
			"ALTER ROLE ALL IN DATABASE rsd_one SET application_name='D'",
			"ALTER ROLE rsd_a SET application_name='R'",
			"ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='RD'",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, cell := range []struct{ db, role, value, source string }{
			{"rsd_one", "rsd_a", "RD", "database user"}, {"rsd_one", "rsd_b", "D", "database"},
			{"rsd_two", "rsd_a", "R", "user"}, {"rsd_two", "rsd_b", "G", "global"},
		} {
			rsDatabaseFresh(t, p, b, route, cell.db, cell.role, cell.value, "9000", cell.source)
		}
		for _, rung := range []struct{ query, value, source string }{
			{"ALTER ROLE rsd_a IN DATABASE rsd_one RESET application_name", "R", "user"},
			{"ALTER ROLE rsd_a RESET ALL", "D", "database"},
			{"ALTER ROLE ALL IN DATABASE rsd_one SET application_name TO DEFAULT", "G", "global"},
		} {
			run(fixture.Exec(rung.query, 0))
			rsDatabaseFresh(t, p, b, route, "rsd_one", "rsd_a", rung.value, "9000", rung.source)
		}
		run(fixture.Exec("ALTER ROLE ALL RESET application_name", 0))
		rsDatabaseFresh(t, p, b, route, "rsd_one", "rsd_a", "", "9000", "default")
	})
}

func TestReferenceRoleSettingsDatabaseAuthority(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		owner, _ := rsDatabasePool(t, p, b, "rsd_two", "rsd_owner")
		other, _ := rsDatabasePool(t, p, b, "rsd_two", "rsd_a")
		run := func(actor fixture.Backend, s fixture.Step) { rsDatabaseRun(t, p, actor, actor.DB, route, s) }
		run(owner, fixture.Exec("ALTER ROLE ALL IN DATABASE rsd_one SET application_name='owned'", 0))
		// Self role defaults do not require ownership of the selected database.
		run(other, fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='self'", 0))
		for _, failure := range []struct{ query, code, message, detail string }{
			{"ALTER ROLE ALL SET application_name='denied'", "42501", "permission denied to alter setting", "Only roles with the SUPERUSER attribute may alter settings globally."},
			{"ALTER ROLE ALL IN DATABASE rsd_one RESET ALL", "42501", "must be owner of database rsd_one", ""},
			{"ALTER ROLE rsd_a IN DATABASE rsd_missing SET application_name='missing'", "3D000", "database \"rsd_missing\" does not exist", ""},
			{"ALTER ROLE rsd_missing IN DATABASE rsd_one RESET ALL", "42704", "role \"rsd_missing\" does not exist", ""},
		} {
			s := fixture.RoleReferenceError(failure.query, failure.code, failure.message, failure.detail, route == "fresh", "execute")
			if route == "held" {
				s.Prepared = false
			}
			run(other, s)
		}
		fresh, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_a")
		run(fresh, rsDatabaseApplication("self"))
		freshB, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_b")
		run(freshB, rsDatabaseApplication("owned"))
	})
}

func TestReferenceRoleSettingsDatabasePhysicalTiming(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		actor, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_a")
		actor.DB.SetMaxOpenConns(1)
		c, err := actor.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		rsDatabaseRun(t, p, actor, c, route, fixture.Exec("SET application_name='local'", 0))
		rsDatabaseRun(t, p, b, b.DB, route, fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='future'", 0))
		rsDatabaseRun(t, p, actor, c, route, rsDatabaseApplication("local"))
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		// Return/reborrow must retain the original physical override, not initialize.
		rsDatabaseRun(t, p, actor, actor.DB, route, rsDatabaseApplication("local"))
		rsDatabaseRun(t, p, actor, actor.DB, route, fixture.Exec("RESET application_name", 0))
		rsDatabaseRun(t, p, actor, actor.DB, route, rsDatabaseApplication(""))
		fresh, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_a")
		rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication("future"))
		rsDatabaseRun(t, p, b, b.DB, route, fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one RESET ALL", 0))
		rsDatabaseRun(t, p, fresh, fresh.DB, route, fixture.Exec("SET application_name='override'", 0))
		rsDatabaseRun(t, p, fresh, fresh.DB, route, fixture.Exec("RESET application_name", 0))
		rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication("future"))
	})
}
