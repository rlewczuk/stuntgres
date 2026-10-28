package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSubscriptionOwnership(t *testing.T) {
	roles := []string{"sub_actor", "sub_target"}
	fixture.RoleReferenceCleanup(t, roles)
	p := subscriptionScenario(t, "sub_owned", "sub_renamed", "sub_password_false")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_actor LOGIN PASSWORD 'role-attributes-fixture'", false)
			subscriptionExec(t, p, b, b.DB, "CREATE ROLE sub_target", false)
			actor := fixture.RoleAttributesPool(t, p, b, "sub_actor")
			create := "CREATE SUBSCRIPTION sub_owned CONNECTION 'password=secret' PUBLICATION remote WITH (connect=false,slot_name=NONE)"
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied to create subscription")
			denied.Detail = `Only roles with privileges of the "pg_create_subscription" role may create subscriptions.`
			step := fixture.ErrorDiagnostic(create, denied)
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, actor, step)
			subscriptionExec(t, p, b, b.DB, "GRANT pg_create_subscription TO sub_actor", false)
			var database string
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			quoted := "\"" + strings.ReplaceAll(database, "\"", "\"\"") + "\""
			description := database
			if !b.PG {
				description = quoted // opaque native attachment contains path punctuation
			}
			subscriptionError(t, p, b, actor, create, "42501", "permission denied for database "+description, false)
			subscriptionExec(t, p, b, b.DB, "GRANT CREATE ON DATABASE "+quoted+" TO sub_actor", false)
			password := fixture.ControlsReferenceDiagnostic("2F003", "password is required")
			password.Detail = "Non-superusers must provide a password in the connection string."
			for _, text := range []string{"", "password=''", "password=secret password=''"} {
				q := "CREATE SUBSCRIPTION sub_owned CONNECTION '" + strings.ReplaceAll(text, "'", "''") + "' PUBLICATION remote WITH (connect=false,slot_name=NONE)"
				step := fixture.ErrorDiagnostic(q, password)
				step.ErrorStage = "execute"
				subscriptionRun(t, p, b, actor, step)
			}
			subscriptionExec(t, p, b, actor, create, true)
			// Keep the same prepared command across each authority transition.
			heldSQL := "ALTER SUBSCRIPTION sub_owned SET (binary=true)"
			held, err := actor.PrepareContext(p.Ctx, heldSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			heldExecutor := fixture.HeldStatement{Executor: actor, Statement: held, SQL: heldSQL}
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_owned OWNER TO sub_target", false)
			failure := fixture.ErrorDiagnostic(heldSQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of subscription sub_owned"))
			failure.ErrorStage = "execute"
			subscriptionRun(t, p, b, heldExecutor, failure)
			subscriptionExec(t, p, b, b.DB, "GRANT sub_target TO sub_actor", false)
			subscriptionRun(t, p, b, heldExecutor, fixture.Exec(heldSQL, 0))
			subscriptionExec(t, p, b, b.DB, "REVOKE sub_target FROM sub_actor", false)
			subscriptionRun(t, p, b, heldExecutor, failure)
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary FROM pg_subscription WHERE subname='sub_owned'", []string{"subbinary"}, []string{"BOOL"}, [][]any{{true}}))
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_owned OWNER TO sub_actor", false)
			held.Close()
			// CREATE also rechecks a revoked predefined-role grant at Execute.
			heldCreate, err := actor.PrepareContext(p.Ctx, create)
			if err != nil {
				t.Fatal(err)
			}
			defer heldCreate.Close()
			subscriptionExec(t, p, b, b.DB, "REVOKE pg_create_subscription FROM sub_actor", false)
			step = fixture.ErrorDiagnostic(create, denied)
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, fixture.HeldStatement{Executor: actor, Statement: heldCreate, SQL: create}, step)
			subscriptionExec(t, p, b, b.DB, "GRANT pg_create_subscription TO sub_actor", false)
			heldCreate.Close()
			subscriptionError(t, p, b, actor, "ALTER SUBSCRIPTION sub_owned OWNER TO sub_target", "42501", `must be able to SET ROLE "sub_target"`, true)
			subscriptionExec(t, p, b, b.DB, "GRANT sub_target TO sub_actor WITH INHERIT FALSE, SET TRUE", false)
			// Target has neither CREATE nor pg_create_subscription; only the actor needs
			// database CREATE and the SET ROLE edge for owner transfer.
			subscriptionExec(t, p, b, actor, "ALTER SUBSCRIPTION sub_owned OWNER TO sub_target", true)
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_owned RENAME TO sub_renamed", true)
			subscriptionError(t, p, b, actor, "ALTER SUBSCRIPTION sub_renamed DISABLE", "42501", "must be owner of subscription sub_renamed", true)
			dep := fixture.ControlsReferenceDiagnostic("2BP01", `role "sub_target" cannot be dropped because some objects depend on it`)
			dep.Detail = "owner of subscription sub_renamed"
			step = fixture.ErrorDiagnostic("DROP ROLE sub_target", dep)
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, b.DB, step)
			subscriptionExec(t, p, b, b.DB, "REASSIGN OWNED BY sub_target TO sub_actor", true)
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT r.rolname FROM pg_subscription s JOIN pg_roles r ON r.oid=s.subowner WHERE s.subname='sub_renamed'", []string{"rolname"}, []string{"NAME"}, [][]any{{"sub_actor"}}))
			subscriptionExec(t, p, b, b.DB, "DROP OWNED BY sub_actor", true)
			subscriptionList(t, p, b, b.DB, "sub_renamed", "{remote}", false)
			subscriptionExec(t, p, b, b.DB, "DROP OWNED BY sub_actor CASCADE", true)
			subscriptionList(t, p, b, b.DB, "sub_renamed", "{remote}", false)
			retained := fixture.ControlsReferenceDiagnostic("2BP01", `role "sub_actor" cannot be dropped because some objects depend on it`)
			retained.Detail = "owner of subscription sub_renamed"
			step = fixture.ErrorDiagnostic("DROP ROLE sub_actor", retained)
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, b.DB, step)
			subscriptionExec(t, p, b, actor, "DROP SUBSCRIPTION sub_renamed", true)
			subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_renamed'", false))
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_password_false CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE,password_required=false)", false)
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_password_false OWNER TO sub_actor", false)
			super := fixture.ControlsReferenceDiagnostic("42501", "password_required=false is superuser-only")
			super.Hint = "Subscriptions with the password_required option set to false may only be created or modified by the superuser."
			step = fixture.ErrorDiagnostic("ALTER SUBSCRIPTION sub_password_false DISABLE", super)
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, actor, step)
			subscriptionExec(t, p, b, actor, "DROP SUBSCRIPTION sub_password_false", true)
			actor.Close()
			subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_actor", false)
			subscriptionExec(t, p, b, b.DB, "DROP ROLE sub_target", false)
		})
	}
}
