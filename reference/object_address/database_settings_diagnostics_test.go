package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSettingsDiagnostics(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		c := dsConn(t, p, b)
		run := func(s fixture.Step) { dsRun(t, p, b, c, route, s) }
		for _, q := range []string{
			"SET application_name='live'", "ALTER DATABASE ds_one SET application_name='keep'",
			"ALTER DATABASE ds_one SET work_mem='1024kB'", "ALTER DATABASE ds_two SET application_name='peer'",
			"ALTER ROLE ds_a SET application_name='role'", "ALTER ROLE ds_a IN DATABASE ds_one SET application_name='both'",
		} {
			run(fixture.Exec(q, 0))
		}
		atomic := func() {
			run(dsRow("ds_one", "{application_name=keep,work_mem=1024kB}"))
			run(dsRow("ds_two", "{application_name=peer}"))
			run(fixture.Select("SELECT CASE WHEN s.setdatabase=0 THEN 'role' ELSE 'both' END AS scope,s.setconfig FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname='ds_a' ORDER BY s.setdatabase", []string{"scope", "setconfig"}, []string{"TEXT", "_TEXT"}, [][]any{{"role", "{application_name=role}"}, {"both", "{application_name=both}"}}))
			run(rsDatabaseApplication("live"))
			dsFresh(t, p, b, route, "ds_one", "", "keep")
			dsFresh(t, p, b, route, "ds_two", "", "peer")
		}
		for _, tc := range []struct{ target, tail, code, message, detail, hint string }{
			{"ds_missing", "SET standard_conforming_strings='o'", "3D000", `database "ds_missing" does not exist`, "", ""},
			{"ds_missing", "RESET ALL", "3D000", `database "ds_missing" does not exist`, "", ""},
			{`"Missing ""db\名"`, "RESET ALL", "3D000", "database \"Missing \"db\\名\" does not exist", "", ""},
			{"ds_one", "SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""},
			{"ds_one", "SET absent_setting FROM CURRENT", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""},
			{"ds_one", "SET application_name TO 'a','b'", "22023", "SET application_name takes only one argument", "", ""},
			{"ds_one", "SET absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", "", ""},
			{"ds_one", "SET standard_conforming_strings='o'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""},
			{"ds_one", "SET statement_timeout='-1'", "22023", `-1 ms is outside the valid range for parameter "statement_timeout" (0 ms .. 2147483647 ms)`, "", ""},
			{"ds_one", "SET statement_timeout='1 MS'", "22023", `invalid value for parameter "statement_timeout": "1 MS"`, "", `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
			{"ds_one", "SET work_mem='1kB'", "22023", `1 kB is outside the valid range for parameter "work_mem" (64 kB .. 2147483647 kB)`, "", ""},
			{"ds_one", "SET work_mem='1 mb'", "22023", `invalid value for parameter "work_mem": "1 mb"`, "", `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`},
			{"ds_one", "SET server_encoding='UTF8'", "55P02", `parameter "server_encoding" cannot be changed`, "", ""},
			{"ds_one", "SET max_connections=100", "55P02", `parameter "max_connections" cannot be changed without restarting the server`, "", ""},
			{"ds_one", "SET log_checkpoints=on", "55P02", `parameter "log_checkpoints" cannot be changed now`, "", ""},
			{"ds_one", "SET ignore_system_indexes=off", "55P02", `parameter "ignore_system_indexes" cannot be set after connection start`, "", ""},
			{"ds_one", `SET "x..y"='v'`, "42602", `invalid configuration parameter name "x..y"`, "Custom parameter names must be two or more simple identifiers separated by dots.", ""},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Detail, d.Hint = tc.detail, tc.hint
			s := fixture.ImplicitError("ALTER DATABASE "+tc.target+" "+tc.tail, d)
			s.ErrorStage = "execute"
			run(s)
			atomic()
		}
		for _, prefix := range []string{
			"ALTER DATABASE ds_one SET application_name='名'", "ALTER DATABASE ds_one SET application_name FROM CURRENT",
			"ALTER DATABASE ds_one SET application_name=DEFAULT", "ALTER DATABASE ds_one RESET application_name", "ALTER DATABASE ds_one RESET ALL",
		} {
			q := prefix + " unexpected_suffix"
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "unexpected_suffix"`)
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "unexpected_suffix")]) + 1)
			s := fixture.ImplicitError(q, d)
			s.ErrorStage = "execute"
			if route != "direct" {
				s.ErrorStage = "prepare"
			}
			run(s)
			atomic()
		}
	})
}

func TestReferenceDatabaseSettingsPermissions(t *testing.T) {
	dsCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		admin := func(s fixture.Step) { dsRun(t, p, b, b.DB, route, s) }
		owner, _ := dsPool(t, p, b, "ds_two", "ds_owner")
		a, _ := dsPool(t, p, b, "ds_two", "ds_a")
		member, _ := dsPool(t, p, b, "ds_two", "ds_b")
		oc, ac, mc := dsConn(t, p, owner), dsConn(t, p, a), dsConn(t, p, member)
		run := func(actor fixture.Backend, e fixture.Executor, s fixture.Step) { dsRun(t, p, actor, e, route, s) }
		admin(fixture.Select("SELECT rolcreatedb FROM pg_roles WHERE rolname='ds_owner'", []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{false}}))
		admin(fixture.Exec("REVOKE CONNECT ON DATABASE ds_one FROM PUBLIC", 0))
		run(owner, oc, fixture.Exec("ALTER DATABASE ds_one SET application_name='owned'", 0))
		for _, tail := range []string{"SET standard_conforming_strings='o'", "RESET ALL"} {
			run(a, ac, dsError("ALTER DATABASE ds_missing "+tail, "3D000", `database "ds_missing" does not exist`))
			admin(dsRow("ds_one", "{application_name=owned}"))
			admin(dsRow("ds_two", ""))
			run(a, ac, rsDatabaseApplication(""))
			dsFresh(t, p, b, route, "ds_one", "", "owned")
		}
		admin(fixture.Exec("GRANT CONNECT, CREATE ON DATABASE ds_one TO ds_a", 0))
		for _, tc := range []struct{ target, message string }{
			{"ds_one", "must be owner of database ds_one"}, {`"Mixed db"`, "must be owner of database Mixed db"},
		} {
			for _, tail := range []string{"SET standard_conforming_strings='o'", "RESET ALL", "SET application_name FROM CURRENT"} {
				run(a, ac, dsError("ALTER DATABASE "+tc.target+" "+tail, "42501", tc.message))
				admin(dsRow("ds_one", "{application_name=owned}"))
				admin(dsRow("Mixed db", ""))
				run(a, ac, rsDatabaseApplication(""))
				dsFresh(t, p, b, route, "ds_one", "", "owned")
			}
		}
		admin(fixture.Exec("GRANT ds_owner TO ds_a WITH INHERIT TRUE", 0))
		run(a, ac, fixture.Exec("ALTER DATABASE ds_one SET application_name='inherited'", 0))
		admin(fixture.Exec("GRANT ds_owner TO ds_b WITH INHERIT FALSE, SET TRUE", 0))
		run(member, mc, dsError("ALTER DATABASE ds_one RESET ALL", "42501", "must be owner of database ds_one"))
		admin(dsRow("ds_one", "{application_name=inherited}"))
		run(member, mc, fixture.Exec("SET ROLE ds_owner", 0))
		run(member, mc, fixture.Exec("ALTER DATABASE ds_one SET application_name='set-role'", 0))
		run(member, mc, fixture.Exec("RESET ROLE", 0))
		for _, tc := range []struct{ name, value string }{{"log_statement_stats", "off"}, {"ds_custom.value", "custom"}} {
			run(owner, oc, dsError("ALTER DATABASE ds_one SET "+tc.name+"='"+tc.value+"'", "42501", `permission denied to set parameter "`+tc.name+`"`))
			admin(dsRow("ds_one", "{application_name=set-role}"))
		}
		run(owner, oc, fixture.Exec("SET ds_custom.value='live-custom'", 0))
		admin(fixture.Exec("GRANT SET ON PARAMETER log_statement_stats, ds_custom.value TO ds_owner", 0))
		run(owner, oc, fixture.Exec("ALTER DATABASE ds_one SET log_statement_stats=off", 0))
		run(owner, oc, fixture.Exec("ALTER DATABASE ds_one SET ds_custom.value='custom'", 0))
		admin(fixture.Exec("REVOKE SET ON PARAMETER log_statement_stats, ds_custom.value FROM ds_owner", 0))
		for _, tail := range []string{"RESET log_statement_stats", "SET log_statement_stats=DEFAULT", "SET log_statement_stats FROM CURRENT"} {
			run(owner, oc, dsError("ALTER DATABASE ds_one "+tail, "42501", `permission denied to set parameter "log_statement_stats"`))
			admin(dsRow("ds_one", "{application_name=set-role,log_statement_stats=off,ds_custom.value=custom}"))
			dsFresh(t, p, b, route, "ds_one", "", "set-role")
		}
		run(owner, oc, fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		admin(dsRow("ds_one", "{log_statement_stats=off,ds_custom.value=custom}"))
		fresh, _ := dsPool(t, p, b, "ds_one", "")
		dsRun(t, p, fresh, fresh.DB, route, fixture.Select("SELECT current_setting('ds_custom.value') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"custom"}}))
		admin(fixture.Exec("ALTER DATABASE ds_one RESET ALL", 0))
		admin(dsRow("ds_one", ""))
	})
}
