package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLOwnership(t *testing.T) {
	eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
		fixture.RoleReferenceCleanup(t, []string{"et_ddl_owner_a", "et_ddl_owner_b", "et_ddl_reader"})
		eventTriggerScenario(t, []string{"et_owned", "et_owned_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, route}
			d.exec("CREATE ROLE et_ddl_owner_a SUPERUSER", "CREATE ROLE et_ddl_owner_b SUPERUSER", "CREATE ROLE et_ddl_reader")
			d.seed("et_owned", "ddl_command_end", "'ALTER TABLE'")
			i := d.identity("et_owned", "ddl_command_end", "O", `{"ALTER TABLE"}`)
			functionOwner := i.owner
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			d.utility("ALTER EVENT TRIGGER et_owned OWNER TO et_ddl_owner_a")
			i.owner = d.role("et_ddl_owner_a")
			d.catalog(c, i)
			d.failure("DROP ROLE et_ddl_owner_a", eventDDLRoleDependency("et_ddl_owner_a", "et_owned"), false)
			d.exec("ALTER ROLE et_ddl_owner_a NOSUPERUSER", "SET SESSION AUTHORIZATION et_ddl_owner_a")
			d.utility("ALTER EVENT TRIGGER et_owned OWNER TO et_ddl_owner_a")
			d.utility("ALTER EVENT TRIGGER et_owned ENABLE REPLICA")
			i.mode = "R"
			d.utility("ALTER EVENT TRIGGER et_owned RENAME TO et_owned_new")
			i.name = "et_owned_new"
			d.catalog(c, i)
			denied := eventTriggerDiagnostic("42501", `permission denied to change owner of event trigger "et_owned_new"`)
			denied.Hint = "The owner of an event trigger must be a superuser."
			d.failure("ALTER EVENT TRIGGER et_owned_new OWNER TO et_ddl_reader", denied, false)
			d.catalog(c, i)
			d.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION et_ddl_reader")
			// Released code's same-owner early return precedes even OwnerOnly.
			d.utility("ALTER EVENT TRIGGER et_owned_new OWNER TO et_ddl_owner_a")
			d.catalog(c, i)
			for _, q := range []string{"ALTER EVENT TRIGGER et_owned_new DISABLE", "ALTER EVENT TRIGGER et_owned_new RENAME TO et_owned", "ALTER EVENT TRIGGER et_owned_new OWNER TO et_ddl_owner_b", "DROP EVENT TRIGGER et_owned_new"} {
				d.failure(q, eventTriggerDiagnostic("42501", "must be owner of event trigger et_owned_new"), false)
				d.catalog(c, i)
			}
			d.failure("ALTER EVENT TRIGGER et_owned_new OWNER TO et_ddl_missing", eventTriggerDiagnostic("42704", `role "et_ddl_missing" does not exist`), false)
			d.failure("ALTER EVENT TRIGGER et_absent OWNER TO et_ddl_missing", eventTriggerDiagnostic("42704", `role "et_ddl_missing" does not exist`), false)
			d.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION et_ddl_owner_a")
			d.utility("ALTER EVENT TRIGGER et_owned_new OWNER TO et_ddl_owner_b")
			i.owner = d.role("et_ddl_owner_b")
			d.failure("ALTER EVENT TRIGGER et_owned_new ENABLE", eventTriggerDiagnostic("42501", "must be owner of event trigger et_owned_new"), false)
			d.exec("RESET SESSION AUTHORIZATION")
			d.catalog(c, i)
			// No function/audit ownership or ACL belongs to a: this proves the old
			// trigger-owner dependency really disappeared rather than being hidden.
			d.utility("DROP ROLE et_ddl_owner_a")
			d.failure("DROP ROLE et_ddl_owner_b", eventDDLRoleDependency("et_ddl_owner_b", "et_owned_new"), false)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc WHERE proname='et_ddl_fn' AND proowner="+fmt.Sprint(functionOwner), 1))
			d.exec("ALTER ROLE et_ddl_owner_b NOSUPERUSER", "SET SESSION AUTHORIZATION et_ddl_owner_b")
			d.utility("ALTER EVENT TRIGGER et_owned_new DISABLE")
			i.mode = "D"
			d.catalog(c, i)
			d.utility("DROP EVENT TRIGGER et_owned_new")
			d.exec("RESET SESSION AUTHORIZATION")
			d.catalog(c)
			d.dependencies(i, false)
			d.utility("DROP ROLE et_ddl_owner_b")
			d.audit()
		})
	})
	for _, spec := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
		t.Run(spec, func(t *testing.T) {
			eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
				fixture.RoleReferenceCleanup(t, []string{"et_ddl_identity_owner"})
				eventTriggerScenario(t, []string{"et_ddl_identity"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					d := eventDDLCase{t, p, b, c, route}
					d.exec("CREATE ROLE et_ddl_identity_owner SUPERUSER")
					d.seed("et_ddl_identity", "ddl_command_end", "'ALTER TABLE'")
					i := d.identity("et_ddl_identity", "ddl_command_end", "O", `{"ALTER TABLE"}`)
					bootstrap := i.owner
					defer c.ExecContext(context.Background(), "RESET ROLE")
					d.exec("SET ROLE et_ddl_identity_owner")
					d.utility("ALTER EVENT TRIGGER et_ddl_identity OWNER TO " + spec)
					if spec != "SESSION_USER" {
						i.owner = d.role("et_ddl_identity_owner")
					}
					d.catalog(c, i)
					d.exec("RESET ROLE", "SET SESSION AUTHORIZATION et_ddl_identity_owner")
					defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					d.utility("ALTER EVENT TRIGGER et_ddl_identity OWNER TO " + spec)
					i.owner = d.role("et_ddl_identity_owner")
					d.catalog(c, i)
					d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc WHERE proname='et_ddl_fn' AND proowner="+fmt.Sprint(bootstrap), 1))
					d.audit()
				})
			})
		})
	}
}
