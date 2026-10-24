package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAlterAtomicity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ra_atomic")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE SCHEMA ra_atomic", "CREATE FUNCTION ra_atomic_old(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION ra_atomic_old(text) RETURNS bigint RETURN 104::bigint", "CREATE FUNCTION ra_atomic_taken(integer) RETURNS bigint RETURN 900::bigint", "CREATE PROCEDURE ra_atomic.ra_atomic_old(integer) LANGUAGE SQL AS $$SELECT $1$$", "CREATE TABLE ra_atomic_data(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_atomic_data VALUES(7)", 1))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_atomic_old(integer)'::regprocedure::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ q, message string }{
			{"ALTER FUNCTION ra_atomic_old(integer) RENAME TO ra_atomic_taken", `function ra_atomic_taken(integer) already exists in schema "public"`},
			{"ALTER FUNCTION ra_atomic_old(integer) RENAME TO ra_atomic_old", `function ra_atomic_old(integer) already exists in schema "public"`},
			{"ALTER FUNCTION ra_atomic_old(integer) SET SCHEMA ra_atomic", `function ra_atomic_old(integer) already exists in schema "ra_atomic"`},
		} {
			routineAlterError(t, p, b, c, tc.q, "42723", tc.message)
			var after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_atomic_old(integer)'::regprocedure::oid").Scan(&after); err != nil || after != oid {
				t.Fatal(after, oid, err)
			}
			routineValue(t, p, b, c, "SELECT ra_atomic_old(4) AS v", "v", "INT4", int64(5))
			routineValue(t, p, b, c, "SELECT ra_atomic_old('x'::text) AS v", "v", "INT8", int64(104))
			routineAlterRows(t, p, b, c, "SELECT v FROM ra_atomic_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		}
		stmt, err := c.PrepareContext(p.Ctx, "ALTER FUNCTION ra_atomic_old(integer) RENAME TO ra_atomic_new")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		routineValue(t, p, b, c, "SELECT ra_atomic_old(4) AS v", "v", "INT4", int64(5))
		routineExec(t, p, b, c, "SET default_transaction_read_only=on")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_missing(integer) RENAME TO bad", "25006", "cannot execute ALTER FUNCTION in a read-only transaction")
		routineExec(t, p, b, c, "SET default_transaction_read_only=off")
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: "ALTER FUNCTION ra_atomic_old(integer) RENAME TO ra_atomic_new"}, b.Recorder, fixture.Exec("ALTER FUNCTION ra_atomic_old(integer) RENAME TO ra_atomic_new", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT ra_atomic_new(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		routineValue(t, p, b, c, "SELECT ra_atomic_old(4) AS v", "v", "INT4", int64(5))
	})
}

func TestReferenceRoutineAlterAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_creator", "ra_schema_owner", "ra_reader", "ra_recipient", "ra_no_create"})
	fixture.SchemaReferenceCleanup(t, "ra_auth", "ra_dest")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE ra_creator", "CREATE ROLE ra_schema_owner", "CREATE ROLE ra_reader", "CREATE ROLE ra_recipient", "CREATE ROLE ra_no_create",
			"CREATE SCHEMA ra_auth AUTHORIZATION ra_schema_owner", "CREATE SCHEMA ra_dest",
			"GRANT USAGE,CREATE ON SCHEMA ra_auth TO ra_creator,ra_recipient",
			"GRANT USAGE ON SCHEMA ra_auth TO ra_reader,ra_no_create",
			"GRANT USAGE ON SCHEMA ra_dest TO ra_creator",
			"SET ROLE ra_creator", "CREATE FUNCTION ra_auth.f(integer) RETURNS integer RETURN $1+1", "RESET ROLE")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// Same-schema SET still checks destination CREATE; OWNER's same-owner
		// shortcut needs lookup USAGE but does not require object ownership.
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_reader")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) SET SCHEMA ra_auth", "42501", "permission denied for schema ra_auth")
		routineExec(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_creator", "RESET SESSION AUTHORIZATION", "GRANT CREATE ON SCHEMA ra_auth TO ra_reader")
		for _, role := range []string{"ra_schema_owner", "ra_reader"} {
			routineExec(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
			routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) RENAME TO bad", "42501", "must be owner of function f")
			// Isolate the no-op ownership shortcut from destination-schema
			// lookup: both actors have CREATE as well as USAGE on ra_auth.
			routineExec(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) SET SCHEMA ra_auth", "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_creator", "RESET SESSION AUTHORIZATION")
		}
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_creator")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) SET SCHEMA ra_dest", "42501", "permission denied for schema ra_dest")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_absent", "42704", `role "ra_absent" does not exist`)
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_recipient", "42501", `must be able to SET ROLE "ra_recipient"`)
		// Prove current-schema CREATE before recipient membership can supply
		// that privilege through INHERIT.
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE CREATE ON SCHEMA ra_auth FROM ra_creator", "SET SESSION AUTHORIZATION ra_creator")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) RENAME TO bad", "42501", "permission denied for schema ra_auth")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT CREATE ON SCHEMA ra_auth TO ra_creator", "SET SESSION AUTHORIZATION ra_creator")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT ra_recipient TO ra_creator WITH INHERIT TRUE, SET FALSE, ADMIN TRUE", "SET SESSION AUTHORIZATION ra_creator")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_recipient", "42501", `must be able to SET ROLE "ra_recipient"`)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT ra_no_create TO ra_creator WITH SET TRUE", "SET SESSION AUTHORIZATION ra_creator")
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_no_create", "42501", "permission denied for schema ra_auth")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT CREATE ON SCHEMA ra_dest TO ra_creator", "GRANT ra_recipient TO ra_creator WITH SET TRUE", "SET SESSION AUTHORIZATION ra_creator", "ALTER FUNCTION ra_auth.f(integer) OWNER TO CURRENT_ROLE", "ALTER FUNCTION ra_auth.f(integer) OWNER TO CURRENT_USER", "ALTER FUNCTION ra_auth.f(integer) OWNER TO SESSION_USER", "ALTER FUNCTION ra_auth.f(integer) OWNER TO ra_recipient", "RESET SESSION AUTHORIZATION")
		routineAlterRows(t, p, b, c, "SELECT proowner='ra_recipient'::regrole AS owned FROM pg_proc WHERE proname='f' AND pronamespace='ra_auth'::regnamespace", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		routineExec(t, p, b, c, "ALTER FUNCTION ra_auth.f(integer) SET SCHEMA ra_dest", "ALTER FUNCTION ra_dest.f(integer) OWNER TO ra_no_create")
	})
}

func TestReferenceRoutineAlterOwnerACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_acl_old", "ra_acl_new", "ra_acl_reader", "ra_acl_sentinel"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE ROLE ra_acl_old", "CREATE ROLE ra_acl_new", "CREATE ROLE ra_acl_reader", "CREATE ROLE ra_acl_sentinel", "GRANT USAGE,CREATE ON SCHEMA public TO ra_acl_old,ra_acl_new", "GRANT USAGE ON SCHEMA public TO ra_acl_reader,ra_acl_sentinel",
			"SET ROLE ra_acl_old", "CREATE FUNCTION ra_acl(integer) RETURNS integer RETURN $1+1", "REVOKE EXECUTE ON FUNCTION ra_acl(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ra_acl(integer) TO ra_acl_reader", "RESET ROLE",
			"ALTER DEFAULT PRIVILEGES FOR ROLE ra_acl_new REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC", "ALTER DEFAULT PRIVILEGES FOR ROLE ra_acl_new GRANT EXECUTE ON FUNCTIONS TO ra_acl_sentinel", "ALTER FUNCTION ra_acl(integer) OWNER TO ra_acl_new")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
			// Default ACLs are database-wide role dependencies, not objects
			// removed by the fixture's public-schema reset.
			if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE ra_acl_new REVOKE EXECUTE ON FUNCTIONS FROM ra_acl_sentinel"); err != nil {
				t.Error(err)
			}
			if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE ra_acl_new GRANT EXECUTE ON FUNCTIONS TO PUBLIC"); err != nil {
				t.Error(err)
			}
		}()
		// proacl/aclexplode are outside the existing native catalog surface.
		// Actual execution distinguishes preserved grants from reapplied recipient
		// defaults; TestRoutineAlterOwnerACLTransfer checks exact tuple rewriting.
		routineAlterRows(t, p, b, c, "SELECT proowner='ra_acl_new'::regrole AS owned FROM pg_proc WHERE proname='ra_acl'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_acl_sentinel")
		routineAlterError(t, p, b, c, "SELECT ra_acl(4)", "42501", "permission denied for function ra_acl")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_acl_reader")
		q := "SELECT ra_acl(4) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		routineDropHeld(t, p, b, c, stmt, q, "INT4", int64(5))
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET ROLE ra_acl_new", "REVOKE EXECUTE ON FUNCTION ra_acl(integer) FROM ra_acl_reader", "RESET ROLE", "SET SESSION AUTHORIZATION ra_acl_reader")
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function ra_acl"))
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "DROP OWNED BY ra_acl_old", "DROP ROLE ra_acl_old")
		routineValue(t, p, b, c, q, "v", "INT4", int64(5))
	})
}
