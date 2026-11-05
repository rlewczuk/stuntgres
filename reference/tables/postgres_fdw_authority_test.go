package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWWarnings(t *testing.T) {
	postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer nc.Close()
		for _, value := range []string{"", " \t ", "postgres_fdw", `"postgres_fdw"`, "POSTGRES_FDW"} {
			notices = nil
			s := fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD extensions "+fileFDWLiteral(value)+")", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, recorder, s)
			if len(notices) != 0 {
				t.Fatalf("installed/empty extension warnings: %#v", notices)
			}
			postgresFDWStored(run, 1417, "extensions="+value)
			run(fixture.Exec("ALTER SERVER pf_server OPTIONS(DROP extensions)", 0))
		}
		value := `pf_missing_one,"pf Missing ""Two"`
		notices = nil
		s := fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD extensions "+fileFDWLiteral(value)+")", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		want := []fixture.Diagnostic{
			{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: `extension "pf_missing_one" is not installed`},
			{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: `extension "pf Missing "Two" is not installed`},
		}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("extension warnings: got %#v want %#v", notices, want)
		}
		postgresFDWStored(run, 1417, "extensions="+value)
		// SplitIdentifierString treats semicolon as part of an unquoted name:
		// only comma/whitespace delimit entries. It is valid, but not installed.
		notices = nil
		s = fixture.Exec("ALTER SERVER pf_server OPTIONS(SET extensions 'foo;bar')", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		want = []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: `extension "foo;bar" is not installed`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("semicolon-name warning: got %#v want %#v", notices, want)
		}
		postgresFDWStored(run, 1417, "extensions=foo;bar")
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(SET extensions "+fileFDWLiteral(value)+")", 0))
		for _, malformed := range []string{"foo; bar", "foo,", `"unterminated`} {
			run(postgresFDWError("ALTER SERVER pf_server OPTIONS(SET extensions "+fileFDWLiteral(malformed)+")", "22023", `parameter "extensions" must be a list of extension names`))
			postgresFDWStored(run, 1417, "extensions="+value)
		}
	})
}

func postgresFDWRestricted(q, option string) fixture.Step {
	message := "password_required=false is superuser-only"
	hint := "User mappings with the password_required option set to false may only be created or modified by the superuser."
	if option != "password_required" {
		message = "sslcert and sslkey are superuser-only"
		hint = "User mappings with the sslcert or sslkey options set may only be created or modified by the superuser."
	}
	s := postgresFDWError(q, "42501", message)
	s.Diagnostic.Hint = hint
	return s
}

func TestReferencePostgresFDWPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pf_actor", "pf_outsider"})
	postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		for _, role := range []string{"pf_actor", "pf_outsider"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		defer func() {
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY pf_actor,pf_outsider CASCADE", "DROP ROLE pf_actor,pf_outsider"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		run(fixture.Exec("GRANT CREATE,USAGE ON SCHEMA public TO pf_actor,pf_outsider", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER pf_server TO pf_actor", 0))
		run(fixture.Exec("CREATE TABLE pf_local(v integer)", 0))
		run(fixture.Exec("INSERT INTO pf_local VALUES(73)", 1))
		q := "SELECT postgres_fdw_validator(ARRAY[$1::text],1418::oid) AS validated"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		stored := func(entries ...string) {
			run(fixture.Select("SELECT umoptions FROM pg_user_mappings WHERE srvname='pf_server' AND usename=current_user", []string{"umoptions"}, []string{"_TEXT"}, [][]any{{relationOptionsArray(entries)}}))
		}
		run(fixture.Exec("SET ROLE pf_actor", 0))
		for _, option := range []string{"password_required", "sslcert", "sslkey"} {
			value := ""
			if option == "password_required" {
				value = "false"
			}
			run(postgresFDWRestricted("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS("+option+" "+fileFDWLiteral(value)+")", option))
			s := postgresFDWRestricted(q, option)
			s.Args = []any{option + "=" + value}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Select("SELECT count(*) FROM pg_user_mappings WHERE srvname='pf_server'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(postgresFDWError("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(password_required 'yes')", "42601", "password_required requires a Boolean value"))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(password_required 'true',password 'explicit',sslpassword '')", 0))
		stored("password_required=true", "password=explicit", "sslpassword=")
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(SET password_required 'false')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(postgresFDWRestricted("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password 'not-published')", "password_required"))
		stored("password_required=false", "password=explicit", "sslpassword=")
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password_required 'true',SET password 'changed')", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER USER MAPPING FOR pf_actor SERVER pf_server OPTIONS(ADD sslcert '',ADD sslkey '')", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(postgresFDWRestricted("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(DROP sslcert)", "sslkey"))
		run(postgresFDWRestricted("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(SET password 'not-published')", "sslcert"))
		stored("password_required=true", "password=changed", "sslpassword=", "sslcert=", "sslkey=")
		run(fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(DROP sslcert,DROP sslkey)", 0))
		run(fileFDWVoid(postgresFDWCall([]string{"sslcert=", "sslkey=", "gssdelegation=not-a-boolean"}, 1417)))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER SERVER pf_server OWNER TO pf_actor", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		run(fixture.Exec("ALTER SERVER pf_server OPTIONS(ADD sslcert '',ADD sslkey '')", 0))
		run(fixture.Exec("SET ROLE pf_outsider", 0))
		run(postgresFDWError("CREATE USER MAPPING FOR CURRENT_USER SERVER pf_server OPTIONS(password_required 'false')", "42501", "permission denied for foreign server pf_server"))
		run(postgresFDWError("ALTER SERVER pf_server OPTIONS(ADD fetch_size '0')", "42501", "must be owner of foreign server pf_server"))
		run(fixture.Exec("RESET ROLE", 0))
		postgresFDWStored(run, 1417, "sslcert=", "sslkey=")
		run(fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION postgres_fdw_validator(text[],oid) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE pf_actor", 0))
		s := postgresFDWError(q, "42501", "permission denied for function postgres_fdw_validator")
		s.Args = []any{"password_required=true"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		s.Args = []any{nil}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
