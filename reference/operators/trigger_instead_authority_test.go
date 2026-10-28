package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func insteadAuthority(t *testing.T) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run("authority-"+name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"io_actor", "io_owner"})
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				userOperatorExec(t, p, b, c, "CREATE ROLE io_actor", "CREATE ROLE io_owner", "GRANT USAGE ON SCHEMA public TO io_actor,io_owner", "CREATE TABLE io_secure(id integer PRIMARY KEY,v integer)", "CREATE VIEW io_secure_view AS SELECT id,v FROM io_secure", "CREATE TABLE io_security_log(actor text,setting text,depth integer)",
					"CREATE FUNCTION io_secure_fn() RETURNS trigger LANGUAGE plpgsql SET application_name='inside-instead' AS $$BEGIN INSERT INTO io_secure VALUES(NEW.id,NEW.v); INSERT INTO io_security_log VALUES(current_user,current_setting('application_name'),pg_trigger_depth()); RETURN NEW; END$$",
					"ALTER FUNCTION io_secure_fn() OWNER TO io_owner", "REVOKE EXECUTE ON FUNCTION io_secure_fn() FROM PUBLIC", "GRANT INSERT,SELECT,TRIGGER ON io_secure_view TO io_actor", "GRANT INSERT ON io_secure,io_security_log TO io_owner", "SET application_name='outside-instead'", "SET SESSION AUTHORIZATION io_actor")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				ddl := "CREATE TRIGGER body INSTEAD OF INSERT ON io_secure_view FOR EACH ROW EXECUTE FUNCTION io_secure_fn()"
				run(fixture.Error(ddl, "42501"))
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION io_secure_fn() TO io_actor", "SET SESSION AUTHORIZATION io_actor")
				run(fixture.Exec(ddl, 0))
				// Install-time EXECUTE is not rechecked at firing; body SQL is invoker SQL.
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION io_secure_fn() FROM io_actor", "SET SESSION AUTHORIZATION io_actor")
				run(fixture.Error("INSERT INTO io_secure_view VALUES(1,10)", "42501"))
				run(fixture.Select("SELECT current_user AS actor,current_setting('application_name') AS setting,pg_trigger_depth() AS depth", []string{"actor", "setting", "depth"}, []string{"NAME", "TEXT", "INT4"}, [][]any{{"io_actor", "outside-instead", int64(0)}}))
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER FUNCTION io_secure_fn() SECURITY DEFINER", "SET SESSION AUTHORIZATION io_actor")
				run(triggerRows("INSERT INTO io_secure_view VALUES(1,10) RETURNING id,v", []any{int64(1), int64(10)}))
				run(fixture.Select("SELECT current_user AS actor,current_setting('application_name') AS setting,pg_trigger_depth() AS depth", []string{"actor", "setting", "depth"}, []string{"NAME", "TEXT", "INT4"}, [][]any{{"io_actor", "outside-instead", int64(0)}}))
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				run(triggerRows("SELECT id,v FROM io_secure", []any{int64(1), int64(10)}))
				run(fixture.Select("SELECT actor,setting,depth FROM io_security_log", []string{"actor", "setting", "depth"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"io_owner", "inside-instead", int64(1)}}))
				userOperatorExec(t, p, b, c, "ALTER TABLE io_secure ENABLE ROW LEVEL SECURITY", "CREATE POLICY deny_insert ON io_secure FOR INSERT TO io_owner WITH CHECK(false)", "SET SESSION AUTHORIZATION io_actor")
				run(fixture.Error("INSERT INTO io_secure_view VALUES(2,20)", "42501"))
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				run(triggerRows("SELECT id,v FROM io_secure", []any{int64(1), int64(10)}))
				run(fixture.Select("SELECT count(*) AS n FROM io_security_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
}
