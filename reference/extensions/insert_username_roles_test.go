package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func insertUsernameRoles(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	insertUsernameSetup(t, p, b, c)
	run := insertUsernameRun(t, p, b, c)
	var session string
	if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&session); err != nil { t.Fatal(err) }
	q := "INSERT INTO iu_data VALUES($1,NULL,NULL) RETURNING actor"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	stamp := func(id int64, actor string) { fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"actor"}, []string{"TEXT"}, [][]any{{actor}}, id)) }
	identity := func(role string) { run(fixture.Select("SELECT current_user,session_user,current_setting('search_path') AS path", []string{"current_user", "session_user", "path"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{role, session, "public"}})) }
	run(fixture.Exec("SET search_path=public", 0))
	run(fixture.Exec("SET ROLE iu_writer", 0)); stamp(1, "iu_writer"); identity("iu_writer")
	run(fixture.Exec("SET ROLE iu_other", 0)); stamp(2, "iu_other")
	run(fixture.Exec("RESET ROLE", 0))
	for _, q := range []string{
		"CREATE FUNCTION iu_wrap(integer) RETURNS text LANGUAGE plpgsql SECURITY DEFINER AS $$DECLARE a text; BEGIN INSERT INTO public.iu_data VALUES($1,NULL,NULL) RETURNING actor INTO a; RETURN a; END$$",
		"ALTER FUNCTION iu_wrap(integer) OWNER TO iu_owner",
		"CREATE FUNCTION iu_invoke(integer) RETURNS text LANGUAGE plpgsql SECURITY INVOKER AS $$DECLARE a text; BEGIN INSERT INTO public.iu_data VALUES($1,NULL,NULL) RETURNING actor INTO a; RETURN a; END$$",
		"ALTER FUNCTION iu_ext.insert_username() OWNER TO iu_other",
		"ALTER FUNCTION iu_ext.insert_username() SET search_path=pg_catalog",
	} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("SET ROLE iu_writer", 0))
	insertUsernameBoth(t, p, b, c, fixture.Select("SELECT iu_wrap(10) AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_owner"}}))
	insertUsernameBoth(t, p, b, c, fixture.Select("SELECT iu_invoke(11) AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_writer"}}))
	identity("iu_writer")
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("ALTER FUNCTION iu_ext.insert_username() SECURITY DEFINER", 0))
	run(fixture.Exec("SET ROLE iu_writer", 0)); stamp(3, "iu_other")
	insertUsernameBoth(t, p, b, c, fixture.Select("SELECT iu_wrap(12) AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_other"}}))
	identity("iu_writer")
	run(fixture.Exec("RESET ROLE", 0))
	// A caught error observes identity/path after the nested native frame unwinds.
	for _, q := range []string{
		"CREATE TABLE iu_role_bad(id integer,actor text)",
		"GRANT INSERT ON iu_role_bad TO iu_owner",
		"CREATE TRIGGER iu_role_bad_stamp BEFORE INSERT ON iu_role_bad FOR EACH ROW EXECUTE FUNCTION iu_ext.insert_username('missing')",
		"CREATE FUNCTION iu_catch() RETURNS text LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN INSERT INTO public.iu_role_bad VALUES(1,NULL); RETURN 'unreachable'; EXCEPTION WHEN SQLSTATE '09000' THEN RETURN current_user::text || ':' || current_setting('search_path'); END$$",
		"ALTER FUNCTION iu_catch() OWNER TO iu_owner",
	} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("SET ROLE iu_writer", 0))
	insertUsernameBoth(t, p, b, c, fixture.Select("SELECT iu_catch() AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{"iu_owner:public"}}))
	identity("iu_writer")
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("ALTER ROLE iu_other RENAME TO iu_renamed", 0))
	run(fixture.Exec("SET ROLE iu_writer", 0)); stamp(4, "iu_renamed"); identity("iu_writer")
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("ALTER FUNCTION iu_ext.insert_username() SECURITY INVOKER", 0))
	run(fixture.Exec("SET ROLE iu_writer", 0)); stamp(5, "iu_writer"); identity("iu_writer")
	run(fixture.Exec("RESET ROLE", 0))
	insertUsernameBoth(t, p, b, c, fixture.Select("SELECT p.prosecdef,p.proconfig,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='iu_ext' AND p.proname='insert_username'", []string{"prosecdef", "proconfig", "rolname"}, []string{"BOOL", "_TEXT", "NAME"}, [][]any{{false, "{search_path=pg_catalog}", "iu_renamed"}}))
}

func TestReferenceInsertUsernameRoles(t *testing.T) { insertUsernameCase(t, insertUsernameRoles) }
