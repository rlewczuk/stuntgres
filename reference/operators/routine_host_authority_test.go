package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostAuthority(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS host_trusted CASCADE", "DROP OWNED BY host_owner,host_reader CASCADE", "DROP ROLE host_reader", "DROP ROLE host_owner")()
		handler, untrusted := "plpython3_call_handler", "plpython3u"
		trustedSource := "assert isinstance(v, str)\nreturn v"
		if !b.PG {
			handler, untrusted, trustedSource = "go_host_call_handler", "go_host", "text-default-v1"
		}
		for _, q := range []string{
			"CREATE ROLE host_owner", "CREATE ROLE host_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO host_owner,host_reader",
			"CREATE TRUSTED LANGUAGE host_trusted HANDLER " + handler,
			"REVOKE ALL ON LANGUAGE host_trusted FROM PUBLIC",
		} {
			hostExec(t, p, b, c, q)
		}
		hostFunction(t, p, b, c, "host_acl", "increment-v1", hostIncrementPython, "", true)
		hostFunction(t, p, b, c, "host_acl_strict", "increment-v1", hostIncrementPython, "STRICT", true)
		q := "SELECT key,value FROM each(host_acl($1::hstore)) ORDER BY key"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
		hostError(t, p, c, "CREATE FUNCTION host_denied(v hstore) RETURNS hstore LANGUAGE "+untrusted+" AS 'return v'", fixture.ControlsReferenceDiagnostic("42501", "permission denied for language "+untrusted))
		hostError(t, p, c, "CREATE FUNCTION host_denied(v hstore) RETURNS hstore LANGUAGE host_trusted AS 'return v'", fixture.ControlsReferenceDiagnostic("42501", "permission denied for language host_trusted"))
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "GRANT USAGE ON LANGUAGE host_trusted TO host_reader")
		hostExec(t, p, b, c, "REVOKE USAGE ON TYPE hstore FROM PUBLIC")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostError(t, p, c, "CREATE FUNCTION host_denied(v hstore) RETURNS hstore LANGUAGE host_trusted AS 'return v'", fixture.ControlsReferenceDiagnostic("42501", "permission denied for type hstore"))
		// Type USAGE constrains declarations, not an already admitted call.
		hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "GRANT USAGE ON TYPE hstore TO PUBLIC")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostExec(t, p, b, c, "CREATE FUNCTION host_trusted_ok(v hstore) RETURNS hstore LANGUAGE host_trusted AS '"+trustedSource+"'")
		hostBag(t, p, b, c, "host_trusted_ok('a=>7,b=>NULL'::hstore)", [][]any{{"a", "7"}, {"b", nil}})
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "REVOKE USAGE ON LANGUAGE host_trusted FROM host_reader")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostBag(t, p, b, c, "host_trusted_ok('a=>7,b=>NULL'::hstore)", [][]any{{"a", "7"}, {"b", nil}})
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION host_acl(hstore),host_acl_strict(hstore) FROM PUBLIC")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostError(t, p, c, "SELECT host_acl('a=>7'::hstore)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_acl"))
		revokedRows, err := stmt.QueryContext(p.Ctx, "a=>7")
		if revokedRows != nil {
			revokedRows.Close()
		}
		fixture.AssertDiagnostic(t, err, new(fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_acl")))
		// Literal strict NULL has no invocation demand; generic bound NULL still
		// initializes a real call site and must admit its current EXECUTE ACL.
		hostRows(t, p, b, c, "SELECT host_acl_strict(NULL::hstore) IS NULL AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
		hostExec(t, p, b, c, "SET plan_cache_mode=force_generic_plan")
		hostError(t, p, c, "SELECT host_acl_strict($1::hstore)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_acl_strict"), nil)
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION host_acl(hstore) TO host_reader,host_owner")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "8"}, {"c", "3"}}, "a=>7")
		hostExec(t, p, b, c, "RESET ROLE")
		for _, q := range []string{
			"CREATE TABLE host_secret(v text)",
			"ALTER TABLE host_secret OWNER TO host_owner",
			"CREATE FUNCTION host_identity() RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET application_name='host-inner' AS $$ BEGIN INSERT INTO host_secret VALUES (current_user); PERFORM host_acl('a=>1'::hstore); RETURN current_user || ':' || current_role || ':' || current_setting('application_name'); END $$",
			"ALTER FUNCTION host_identity() OWNER TO host_owner",
			"SET application_name='host-outer'", "SET ROLE host_reader",
		} {
			hostExec(t, p, b, c, q)
		}
		hostRows(t, p, b, c, "SELECT host_identity() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"host_owner:host_owner:host-inner"}})
		hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"host_reader", "host_reader", "host-outer"}})
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "ALTER FUNCTION host_identity() SECURITY INVOKER")
		hostExec(t, p, b, c, "SET ROLE host_reader")
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table host_secret")
		d.Context = "SQL statement \"INSERT INTO host_secret VALUES (current_user)\"\nPL/pgSQL function host_identity() line 1 at SQL statement"
		hostError(t, p, c, "SELECT host_identity()", d)
		hostExec(t, p, b, c, "RESET ROLE")
		// Current owner, not the owner captured by a prepared caller, controls
		// SECURITY DEFINER. Grant real access before changing ownership.
		hostExec(t, p, b, c, "GRANT INSERT ON host_secret TO host_reader")
		hostExec(t, p, b, c, "ALTER FUNCTION host_identity() SECURITY DEFINER")
		hostExec(t, p, b, c, "ALTER FUNCTION host_identity() OWNER TO host_reader")
		hostRows(t, p, b, c, "SELECT host_identity() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"host_reader:host_reader:host-inner"}})
		hostRows(t, p, b, c, "SELECT v FROM host_secret ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"host_owner"}, {"host_reader"}})
		for _, volatility := range []string{"STABLE", "IMMUTABLE"} {
			hostExec(t, p, b, c, "ALTER FUNCTION host_identity() "+volatility)
			d := fixture.ControlsReferenceDiagnostic("0A000", "INSERT is not allowed in a non-volatile function")
			d.Context = "SQL statement \"INSERT INTO host_secret VALUES (current_user)\"\nPL/pgSQL function host_identity() line 1 at SQL statement"
			hostError(t, p, c, "SELECT host_identity()", d)
		}
		hostExec(t, p, b, c, "ALTER FUNCTION host_identity() VOLATILE")
		hostRows(t, p, b, c, "SELECT host_identity() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"host_reader:host_reader:host-inner"}})
	})
}
