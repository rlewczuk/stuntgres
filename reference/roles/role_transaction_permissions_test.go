package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalPermissions(t *testing.T) {
	roleTransactionCases(t, []string{"rp_actor", "rp_target", "rp_other", "rp_power", "rp_child", "rp_denied"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held bool, mode string, commit bool) {
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE rp_actor LOGIN CREATEROLE", "CREATE ROLE rp_target", "CREATE ROLE rp_other", "CREATE ROLE rp_power CREATEROLE",
			"CREATE TABLE rp_protected(i INT)", "GRANT SELECT ON rp_protected TO rp_target", "GRANT USAGE ON SCHEMA public TO rp_actor",
			"GRANT rp_target TO rp_actor WITH ADMIN TRUE, INHERIT TRUE, SET TRUE", "GRANT rp_power TO rp_actor",
		} {
			run(c, fixture.Exec(q, 0))
		}
		run(c, fixture.Exec("INSERT INTO rp_protected VALUES(7)", 1))
		e, finish := roleTransactionBegin(t, p, b, c, mode)
		utility := func(q string) { roleTransactionStep(t, p, b, e, held, fixture.Exec(q, 0)) }
		read := fixture.Select("SELECT i FROM rp_protected", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
		denied := func(q, message, detail string) {
			if mode != "autocommit" {
				run(e, fixture.Exec("SAVEPOINT permission_mark", 0))
			}
			roleTransactionStep(t, p, b, e, held, fixture.RoleReferenceError(q, "42501", message, detail, false, "execute"))
			if mode != "autocommit" {
				run(e, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(e, fixture.Exec("ROLLBACK TO permission_mark", 0))
				run(e, fixture.Exec("RELEASE permission_mark", 0))
			}
		}
		utility("SET SESSION AUTHORIZATION rp_actor")
		run(e, read)
		utility("CREATE ROLE rp_child")
		utility("GRANT rp_target TO rp_other")
		utility("REVOKE rp_target FROM rp_other")
		utility("SET ROLE rp_target")
		utility("RESET ROLE")
		utility("RESET SESSION AUTHORIZATION")
		// Omitted ALTER fields preserve LOGIN and the membership graph.
		utility("ALTER ROLE rp_actor NOCREATEROLE")
		utility("GRANT rp_target TO rp_actor WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		utility("SET SESSION AUTHORIZATION rp_actor")
		denied("CREATE ROLE rp_denied", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles.")
		message, detail := membershipDenied("rp_target", "grant")
		denied("GRANT rp_target TO rp_other", message, detail)
		denied("SELECT i FROM rp_protected", "permission denied for table rp_protected", "")
		denied("SET ROLE rp_target", `permission denied to set role "rp_target"`, "")
		run(e, roleTransactionIdentity("rp_actor", "rp_actor"))
		utility("RESET SESSION AUTHORIZATION")
		run(e, fixture.Select("SELECT rolcanlogin,rolcreaterole FROM pg_catalog.pg_roles WHERE rolname='rp_actor'", []string{"rolcanlogin", "rolcreaterole"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		if mode != "autocommit" {
			utility("SAVEPOINT successful_options")
			utility("ALTER ROLE rp_actor CREATEROLE")
			utility("GRANT rp_target TO rp_actor WITH ADMIN TRUE, INHERIT TRUE, SET TRUE")
			utility("SET SESSION AUTHORIZATION rp_actor")
			run(e, read)
			utility("SET ROLE rp_target")
			utility("RESET ROLE")
			utility("RESET SESSION AUTHORIZATION")
			utility("ROLLBACK TO successful_options")
			utility("RELEASE successful_options")
			run(e, fixture.Select("SELECT rolcreaterole FROM pg_catalog.pg_roles WHERE rolname='rp_actor'", []string{"rolcreaterole"}, []string{"BOOL"}, [][]any{{false}}))
			utility("SET SESSION AUTHORIZATION rp_actor")
			denied("SELECT i FROM rp_protected", "permission denied for table rp_protected", "")
			utility("RESET SESSION AUTHORIZATION")
		}
		if mode != "autocommit" {
			// RELEASE is not a commit; an outer rollback must still restore all options.
			run(e, fixture.Exec("SAVEPOINT released", 0))
			utility("ALTER ROLE rp_actor CONNECTION LIMIT 4")
			run(e, fixture.Exec("RELEASE released", 0))
		}
		finish(commit)
		run(c, fixture.Select("SELECT rolcreaterole FROM pg_catalog.pg_roles WHERE rolname='rp_actor'", []string{"rolcreaterole"}, []string{"BOOL"}, [][]any{{!commit}}))
		run(c, roleTransactionExists("rp_child", commit))
		// Real trusted physical LOGIN actor proves special attributes are not inherited.
		pool := roleTransactionLoginPool(t, p, b, "rp_actor")
		actor, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer actor.Close()
		run(actor, roleTransactionIdentity("rp_actor", "rp_actor"))
		if commit {
			roleTransactionStep(t, p, b, actor, held, fixture.RoleReferenceError("CREATE ROLE rp_denied", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles.", false, "execute"))
		} else {
			roleTransactionStep(t, p, b, actor, held, read)
			roleTransactionStep(t, p, b, actor, held, fixture.Exec("CREATE ROLE rp_child", 0))
		}
	})
}
