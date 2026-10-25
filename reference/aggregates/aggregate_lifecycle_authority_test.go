package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateLifecycleAuthority(t *testing.T) {
	t.Run("ownership", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"al_creator", "al_schema_owner", "al_reader", "al_recipient", "al_no_create", "current_user"})
		fixture.SchemaReferenceCleanup(t, "al_auth", "al_dest")
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			lifecycleExec(t, p, b, c, "CREATE ROLE al_creator", "CREATE ROLE al_schema_owner", "CREATE ROLE al_reader", "CREATE ROLE al_recipient", "CREATE ROLE al_no_create", `CREATE ROLE "current_user"`,
				"CREATE SCHEMA al_auth AUTHORIZATION al_schema_owner", "CREATE SCHEMA al_dest",
				"GRANT USAGE,CREATE ON SCHEMA al_auth TO al_creator,al_recipient", "GRANT USAGE ON SCHEMA al_auth TO al_reader,al_no_create", "GRANT USAGE ON SCHEMA al_dest TO al_creator",
				"SET ROLE al_creator", "CREATE AGGREGATE al_auth.a(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')", "RESET ROLE")
			for _, role := range []string{"al_reader", "al_schema_owner"} {
				lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
				lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) RENAME TO bad", "42501", "must be owner of function a")
				lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) OWNER TO al_creator", "RESET SESSION AUTHORIZATION")
			}
			lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION al_creator")
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) SET SCHEMA al_dest", "42501", "permission denied for schema al_dest")
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) OWNER TO al_absent", "42704", `role "al_absent" does not exist`)
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) OWNER TO al_recipient", "42501", `must be able to SET ROLE "al_recipient"`)
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE CREATE ON SCHEMA al_auth FROM al_creator", "SET SESSION AUTHORIZATION al_creator")
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) RENAME TO bad", "42501", "permission denied for schema al_auth")
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT CREATE ON SCHEMA al_auth TO al_creator", "GRANT al_no_create TO al_creator WITH SET TRUE", "SET SESSION AUTHORIZATION al_creator")
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) OWNER TO al_no_create", "42501", "permission denied for schema al_auth")
			lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_auth.a(integer) OWNER TO CURRENT_ROLE", "ALTER AGGREGATE al_auth.a(integer) OWNER TO CURRENT_USER", "ALTER AGGREGATE al_auth.a(integer) OWNER TO SESSION_USER", "RESET SESSION AUTHORIZATION",
				`GRANT USAGE,CREATE ON SCHEMA al_auth TO "current_user"`, `ALTER AGGREGATE al_auth.a(integer) OWNER TO "current_user"`)
			createAggregateBoth(t, p, b, c, fixture.Select(`SELECT proowner='"current_user"'::regrole AS owned FROM pg_proc WHERE proname='a' AND pronamespace='al_auth'::regnamespace`, []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
			lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION al_schema_owner", "DROP AGGREGATE al_auth.a(integer)", "RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("live_acl", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"al_old", "al_new", "al_reader"})
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			lifecycleExec(t, p, b, c,
				"CREATE ROLE al_old", "CREATE ROLE al_new", "CREATE ROLE al_reader",
				"GRANT USAGE,CREATE ON SCHEMA public TO al_old,al_new", "GRANT USAGE ON SCHEMA public TO al_reader",
				"CREATE FUNCTION al_private_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
				"REVOKE EXECUTE ON FUNCTION al_private_step(integer,integer) FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION al_private_step(integer,integer) TO al_old",
				"SET ROLE al_old", "CREATE AGGREGATE al_acl(integer) (SFUNC=al_private_step,STYPE=int4,INITCOND='0')",
				"REVOKE EXECUTE ON FUNCTION al_acl(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION al_acl(integer) TO al_reader",
				"RESET ROLE")
			id := lifecycleOID(t, p, c, "al_acl(integer)")
			supportID := lifecycleOID(t, p, c, "al_private_step(integer,integer)")
			currentOwner := "al_old"
			lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION al_reader")
			const q = "SELECT al_acl(x) AS v FROM(VALUES(1),(2),(3)) s(x) WHERE $1::boolean"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			identity := func() {
				if lifecycleOID(t, p, c, "al_acl(integer)") != id || lifecycleOID(t, p, c, "al_private_step(integer,integer)") != supportID {
					t.Fatal("owner/support grants replaced a routine identity")
				}
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT proowner=$2::regrole AS owned FROM pg_proc WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, id, currentOwner))
			}
			values := func() {
				identity()
				for _, tc := range []struct {
					populated bool
					value     int64
				}{{true, 6}, {false, 0}} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, tc.value, tc.populated))
					createAggregateBoth(t, p, b, c, createAggregateValue(q, tc.value, tc.populated))
				}
			}
			denied := func(message string) {
				identity()
				for _, populated := range []bool{true, false} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message}, populated)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					createAggregateBoth(t, p, b, c, s)
				}
			}
			// The reader never receives support EXECUTE. Before transfer, only
			// the old aggregate owner can authorize this private transition.
			values()
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER AGGREGATE al_acl(integer) OWNER TO al_new", "SET SESSION AUTHORIZATION al_reader")
			currentOwner = "al_new"
			// The old owner's support grant cannot authorize the new owner,
			// including startup of the same previously executed empty plan.
			denied("permission denied for function al_private_step")
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION",
				"GRANT EXECUTE ON FUNCTION al_private_step(integer,integer) TO al_new",
				"REVOKE EXECUTE ON FUNCTION al_private_step(integer,integer) FROM al_old",
				"SET SESSION AUTHORIZATION al_reader")
			values()
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION",
				"REVOKE EXECUTE ON FUNCTION al_private_step(integer,integer) FROM al_new",
				"SET SESSION AUTHORIZATION al_reader")
			denied("permission denied for function al_private_step")
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION",
				"GRANT EXECUTE ON FUNCTION al_private_step(integer,integer) TO al_new",
				"SET SESSION AUTHORIZATION al_reader")
			values()
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET ROLE al_new",
				"REVOKE EXECUTE ON FUNCTION al_acl(integer) FROM al_reader", "RESET ROLE", "SET SESSION AUTHORIZATION al_reader")
			denied("permission denied for aggregate al_acl")
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION al_old")
			lifecycleError(t, p, b, c, "ALTER AGGREGATE al_acl(integer) RENAME TO bad", "42501", "must be owner of function al_acl")
			lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION al_acl(integer) TO al_reader")
			if lifecycleOID(t, p, c, "al_acl(integer)") != id {
				t.Fatal("owner/ACL changes replaced aggregate identity")
			}
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT proowner='al_new'::regrole AS owned FROM pg_proc WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, id))
			lifecycleExec(t, p, b, c, "SET SESSION AUTHORIZATION al_reader")
			values()
		})
	})
}
