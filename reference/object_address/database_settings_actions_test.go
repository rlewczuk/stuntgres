package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsActions(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		c := dsConn(t, p, b)
		run := func(s fixture.Step) { dsRun(t, p, b, c, route, s) }
		run(fixture.Exec("SET application_name='issuer'", 0))
		for _, value := range []string{"D", "E"} {
			run(fixture.Exec("ALTER DATABASE ds_one SET application_name='"+value+"'", 0))
			run(dsRow("ds_one", "{application_name="+value+"}"))
			run(rsDatabaseApplication("issuer"))
			dsFresh(t, p, b, route, "ds_one", "", value)
		}
		for _, tail := range []string{"SET application_name=DEFAULT", "RESET application_name", "RESET ALL"} {
			run(fixture.Exec("ALTER DATABASE ds_one "+tail, 0))
			run(dsRow("ds_one", ""))
			dsFresh(t, p, b, route, "ds_one", "", "")
			run(fixture.Exec("ALTER DATABASE ds_one SET application_name='again'", 0))
		}
		run(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		// The exact absent row bypasses unknown RESET/DEFAULT validation, even
		// when a role-only setting exists. A nonempty database row does not.
		run(fixture.Exec("ALTER ROLE ds_a SET application_name='role-only'", 0))
		for _, tail := range []string{"RESET absent_setting", "SET absent_setting=DEFAULT"} {
			run(fixture.Exec("ALTER DATABASE ds_one "+tail, 0))
			run(dsRow("ds_one", ""))
		}
		run(fixture.Exec("ALTER DATABASE ds_one SET application_name='keep'", 0))
		for _, tail := range []string{"RESET absent_setting", "SET absent_setting=DEFAULT"} {
			run(dsError("ALTER DATABASE ds_one "+tail, "42704", `unrecognized configuration parameter "absent_setting"`))
			run(dsRow("ds_one", "{application_name=keep}"))
		}
		run(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		for _, tc := range []struct{ tail, config, show, value string }{
			{"SET work_mem='1024kB'", "{work_mem=1024kB}", "work_mem", "1MB"},
			{"SET standard_conforming_strings=off", "{standard_conforming_strings=off}", "standard_conforming_strings", "off"},
			{"SET statement_timeout='2s'", "{statement_timeout=2s}", "statement_timeout", "2s"},
			{"SET TIME ZONE 'UTC'", "{TimeZone=UTC}", "TimeZone", "UTC"},
			{"SET search_path TO public, pg_catalog", `{"search_path=public, pg_catalog"}`, "search_path", "public, pg_catalog"},
		} {
			run(fixture.Exec("ALTER DATABASE ds_one "+tc.tail, 0))
			run(dsRow("ds_one", tc.config))
			fresh, raw := dsPool(t, p, b, "ds_one", "")
			dsRun(t, p, fresh, fresh.DB, route, fixture.Select("SHOW "+tc.show, []string{tc.show}, []string{"TEXT"}, [][]any{{tc.value}}))
			if tc.show == "work_mem" {
				dsRun(t, p, fresh, fresh.DB, route, fixture.Select("SELECT setting,reset_val,source FROM pg_settings WHERE name='work_mem'", []string{"setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"1024", "1024", "database"}}))
			}
			fresh.DB.Close()
			raw.Close()
			run(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		}
		// Catalog spelling is canonical even when subsequent SQL changes case
		// or uses the interoperable ALTER ROLE ALL spelling of the same row.
		for _, tc := range []struct{ query, config string }{
			{`ALTER DATABASE ds_one SET "timezone"='UTC'`, "{TimeZone=UTC}"},
			{`ALTER ROLE ALL IN DATABASE ds_one SET "TIMEZONE"='GMT'`, "{TimeZone=GMT}"},
			{`ALTER DATABASE ds_one RESET "TimeZone"`, ""},
			{`ALTER DATABASE ds_one SET "TIMEZONE"='GMT'`, "{TimeZone=GMT}"},
			{`ALTER ROLE ALL IN DATABASE ds_one SET "timezone"=DEFAULT`, ""},
		} {
			run(fixture.Exec(tc.query, 0))
			run(dsRow("ds_one", tc.config))
		}
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec(`ALTER DATABASE ds_one SET "TIMEZONE" FROM CURRENT`, 0))
		run(dsRow("ds_one", "{TimeZone=UTC}"))
		run(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		run(dsRow("ds_one", ""))
		run(fixture.Exec("SET work_mem='1024kB'", 0))
		run(fixture.Exec("ALTER DATABASE ds_one SET work_mem FROM CURRENT", 0))
		run(dsRow("ds_one", "{work_mem=1MB}"))
	})
}

func TestReferenceDatabaseSettingsCatalog(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		run := func(s fixture.Step) { dsRun(t, p, b, b.DB, route, s) }
		for _, q := range []string{
			"ALTER DATABASE ds_two SET application_name='peer'",
			"ALTER ROLE ds_a SET application_name='role'",
			"ALTER ROLE ds_a IN DATABASE ds_one SET application_name='both'",
			"ALTER DATABASE ds_one SET application_name='database'",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ query, config string }{
			{"ALTER ROLE ALL IN DATABASE ds_one SET application_name='role-spelling'", "{application_name=role-spelling}"},
			{"ALTER DATABASE ds_one SET work_mem='1024kB'", "{application_name=role-spelling,work_mem=1024kB}"},
			{"ALTER ROLE ALL IN DATABASE ds_one RESET application_name", "{work_mem=1024kB}"},
			{"ALTER DATABASE ds_one RESET ALL", ""},
			{"ALTER ROLE ALL IN DATABASE ds_one SET application_name='again'", "{application_name=again}"},
			{"ALTER DATABASE ds_one RESET application_name", ""},
		} {
			run(fixture.Exec(tc.query, 0))
			run(dsRow("ds_one", tc.config))
			run(dsRow("ds_two", "{application_name=peer}"))
			run(fixture.Select("SELECT CASE WHEN s.setdatabase=0 THEN 'role' ELSE 'both' END AS scope,s.setconfig FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname='ds_a' ORDER BY s.setdatabase", []string{"scope", "setconfig"}, []string{"TEXT", "_TEXT"}, [][]any{{"role", "{application_name=role}"}, {"both", "{application_name=both}"}}))
		}
	})
}

func TestReferenceDatabaseSettingsSessions(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		a, _ := dsPool(t, p, b, "ds_one", "")
		peer, _ := dsPool(t, p, b, "ds_two", "")
		a.DB.SetMaxOpenConns(1)
		peer.DB.SetMaxOpenConns(1)
		old := dsConn(t, p, a)
		run := func(s fixture.Step) { dsRun(t, p, a, old, route, s) }
		run(rsDatabaseApplication(""))
		dsRun(t, p, peer, peer.DB, route, rsDatabaseApplication(""))
		dsRun(t, p, peer, peer.DB, route, fixture.Exec("ALTER DATABASE ds_one SET application_name='D'", 0))
		run(rsDatabaseApplication(""))
		run(fixture.Exec("SET application_name='local'", 0))
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		dsRun(t, p, a, a.DB, route, rsDatabaseApplication("local"))
		dsRun(t, p, a, a.DB, route, fixture.Exec("RESET application_name", 0))
		dsRun(t, p, a, a.DB, route, rsDatabaseApplication(""))
		fresh, _ := dsPool(t, p, b, "ds_one", "")
		fresh.DB.SetMaxOpenConns(1)
		dsRun(t, p, fresh, fresh.DB, route, dsSetting("D", "database"))
		dsRun(t, p, peer, peer.DB, route, fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		dsRun(t, p, fresh, fresh.DB, route, fixture.Exec("SET application_name='override'", 0))
		dsRun(t, p, fresh, fresh.DB, route, fixture.Exec("RESET application_name", 0))
		dsRun(t, p, fresh, fresh.DB, route, dsSetting("D", "database"))
		dsRun(t, p, peer, peer.DB, route, rsDatabaseApplication(""))
		dsFresh(t, p, b, route, "ds_one", "", "")
		dsFresh(t, p, b, route, "ds_two", "", "")
	})
}

func TestReferenceDatabaseSettingsPrecedence(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		issuer := dsConn(t, p, b)
		run := func(s fixture.Step) { dsRun(t, p, b, issuer, route, s) }
		run(rsDatabaseApplication(""))
		for _, q := range []string{
			"ALTER ROLE ALL SET application_name='G'", "ALTER DATABASE ds_one SET application_name='D'",
			"ALTER ROLE ds_a SET application_name='R'", "ALTER ROLE ds_a IN DATABASE ds_one SET application_name='RD'",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ db, role, value, source string }{
			{"ds_one", "ds_a", "RD", "database user"}, {"ds_one", "ds_b", "D", "database"},
			{"ds_two", "ds_a", "R", "user"}, {"ds_two", "ds_b", "G", "global"},
		} {
			actor, raw := dsPool(t, p, b, tc.db, tc.role)
			dsRun(t, p, actor, actor.DB, route, dsSetting(tc.value, tc.source))
			actor.DB.Close()
			raw.Close()
		}
		actor, _ := dsPool(t, p, b, "ds_one", "ds_a")
		c := dsConn(t, p, actor)
		run(fixture.Exec("GRANT ds_b TO ds_a", 0))
		dsRun(t, p, actor, c, route, fixture.Exec("SET ROLE ds_b", 0))
		dsRun(t, p, actor, c, route, dsSetting("RD", "database user"))
		run(fixture.Exec("SET SESSION AUTHORIZATION ds_a", 0))
		run(rsDatabaseApplication(""))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		dsRun(t, p, actor, c, route, fixture.Exec("RESET ROLE", 0))
		for _, rung := range []struct{ query, value, source string }{
			{"ALTER ROLE ds_a IN DATABASE ds_one RESET ALL", "R", "user"},
			{"ALTER ROLE ds_a RESET ALL", "D", "database"},
			{"ALTER DATABASE ds_one RESET application_name", "G", "global"},
			{"ALTER ROLE ALL RESET ALL", "", "default"},
		} {
			run(fixture.Exec(rung.query, 0))
			fresh, raw := dsPool(t, p, b, "ds_one", "ds_a")
			dsRun(t, p, fresh, fresh.DB, route, dsSetting(rung.value, rung.source))
			fresh.DB.Close()
			raw.Close()
		}
	})
}
