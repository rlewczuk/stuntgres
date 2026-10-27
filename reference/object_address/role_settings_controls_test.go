package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsControlsRetention(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		run := func(s fixture.Step) { rsDatabaseRun(t, p, b, b.DB, route, s) }
		// Both recorded and raw pools can own physical sessions. Track every
		// selected, login and control-observation handle, not just the first pair.
		pools := make(map[*sql.DB]struct{})
		open := func(database, role string) (fixture.Backend, *sql.DB) {
			actor, raw := rsDatabasePool(t, p, b, database, role)
			pools[actor.DB], pools[raw] = struct{}{}, struct{}{}
			return actor, raw
		}
		closePools := func() {
			for pool := range pools {
				if err := pool.Close(); err != nil {
					t.Fatal(err)
				}
				delete(pools, pool)
			}
		}
		for _, q := range []string{
			"ALTER ROLE ALL SET application_name='G'",
			"ALTER ROLE ALL IN DATABASE rsd_one SET application_name='D'",
			"ALTER ROLE rsd_a SET application_name='R'",
			"ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='RD'",
		} {
			run(fixture.Exec(q, 0))
		}
		selected, raw := open("rsd_one", "")
		rsDatabaseRun(t, p, selected, selected.DB, route, fixture.Exec("CREATE TABLE rsd_payload(v integer)", 0))
		rsDatabaseRun(t, p, selected, selected.DB, route, fixture.Exec("INSERT INTO rsd_payload VALUES(42)", 1))
		login, _ := open("rsd_one", "rsd_a")
		login.DB.SetMaxOpenConns(1)
		rsDatabaseRun(t, p, login, login.DB, route, rsDatabaseApplication("RD"))
		sibling, _ := open("rsd_two", "rsd_a")
		rsDatabaseRun(t, p, sibling, sibling.DB, route, rsDatabaseApplication("R"))
		// Controls have no PostgreSQL analogue; all surrounding SQL is shared.
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, raw, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "ALTER ROLE") || strings.Contains(dump.String(), "ALTER USER") || strings.Contains(dump.String(), "pg_db_role_setting") {
				t.Fatal("dump imported cluster settings authority")
			}
			if err := stuntgres.Reset(p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			rsDatabaseRun(t, p, login, login.DB, route, rsDatabaseApplication("RD"))
			fresh, _ := open("rsd_one", "rsd_a")
			rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication("RD"))
			// The destination changes after capture: Restore must not restore RD.
			run(fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='destination'", 0))
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			after, _ := open("rsd_one", "rsd_a")
			rsDatabaseRun(t, p, after, after.DB, route, rsDatabaseApplication("destination"))
			rsDatabaseRun(t, p, login, login.DB, route, rsDatabaseApplication("RD"))
			rsDatabaseRun(t, p, sibling, sibling.DB, route, rsDatabaseApplication("R"))
			rsDatabaseRun(t, p, selected, selected.DB, route, fixture.Select("SELECT v FROM rsd_payload", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			// Another cluster's Dump cannot carry defaults into this destination.
			driver := new(stuntgres.Driver)
			connector, err := driver.OpenConnector("role-settings-import-source")
			if err != nil {
				t.Fatal(err)
			}
			source := sql.OpenDB(connector)
			t.Cleanup(func() { source.Close() })
			if _, err := source.ExecContext(p.Ctx, "CREATE TABLE rsd_import(v integer)"); err != nil {
				t.Fatal(err)
			}
			if _, err := source.ExecContext(p.Ctx, "ALTER ROLE ALL SET application_name='foreign'"); err != nil {
				t.Fatal(err)
			}
			pools[source] = struct{}{}
			var foreign bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, source, &foreign); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(foreign.Bytes())); err != nil {
				t.Fatal(err)
			}
			afterImport, _ := open("rsd_one", "rsd_a")
			rsDatabaseRun(t, p, afterImport, afterImport.DB, route, rsDatabaseApplication("destination"))
			freshSibling, _ := open("rsd_two", "rsd_a")
			rsDatabaseRun(t, p, freshSibling, freshSibling.DB, route, rsDatabaseApplication("R"))
			controlLogin, _ := open("rsd_one", "rsd_b")
			rsDatabaseRun(t, p, controlLogin, controlLogin.DB, route, rsDatabaseApplication("D"))
			rsDatabaseRun(t, p, selected, selected.DB, route, fixture.Select("SELECT count(*) AS n FROM rsd_import", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='RD'", 0))
		}
		// No test-created pool for either selected database remains open here.
		// Reopen via the same scenario Driver key, rather than creating a Driver.
		// The fixture's bootstrap observation pool is intentionally still alive.
		closePools()
		for _, cell := range []struct{ database, role, value string }{
			{"rsd_one", "rsd_a", "RD"}, {"rsd_two", "rsd_a", "R"}, {"rsd_one", "rsd_b", "D"}, {"rsd_two", "rsd_b", "G"},
		} {
			fresh, _ := open(cell.database, cell.role)
			rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(cell.value))
			closePools()
		}
		// Defaults are cleanup data, not DROP dependencies; names cannot revive them.
		run(fixture.Exec("DROP ROLE rsd_a", 0))
		run(fixture.Exec("CREATE ROLE rsd_a LOGIN PASSWORD 'role-core-fixture'", 0))
		for _, cell := range []struct{ database, value string }{{"rsd_one", "D"}, {"rsd_two", "G"}} {
			fresh, _ := open(cell.database, "rsd_a")
			rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(cell.value))
			closePools()
		}
		run(rsDatabaseRows("RD", 0))
		run(rsDatabaseRows("R", 0))
	})
}
