package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleSettingsTransactionHeldMutation(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		issuer, _ := fixture.RoleTransactionConn(t, p, b)
		peer, _ := rsDatabasePool(t, p, b, "rsd_two", "")
		run := func(s fixture.Step) { rsDatabaseRun(t, p, b, issuer, route, s) }
		for _, q := range []string{
			"ALTER ROLE rsd_a SET application_name='before'",
			"ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='database'",
			"ALTER ROLE rsd_b SET application_name='other'",
		} {
			run(fixture.Exec(q, 0))
		}
		query := "ALTER ROLE rsd_a SET application_name FROM CURRENT"
		stmt, err := issuer.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: issuer, Statement: stmt, SQL: query}
		// This one physical statement survives every issuer, value and transaction
		// transition below. Do not route it through the fresh-prepare helper.
		execute := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		rows := func(value string) fixture.Step {
			return fixture.Select(
				"SELECT r.rolname,CASE WHEN s.setdatabase=0 THEN 'role' ELSE 'database' END AS scope,s.setconfig::text FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname IN ('rsd_a','rsd_b') ORDER BY r.rolname,s.setdatabase",
				[]string{"rolname", "scope", "setconfig"}, []string{"NAME", "TEXT", "TEXT"},
				[][]any{{"rsd_a", "role", "{application_name=" + value + "}"}, {"rsd_a", "database", "{application_name=database}"}, {"rsd_b", "role", "{application_name=other}"}})
		}
		observePeer := func(value string) {
			rsDatabaseRun(t, p, peer, peer.DB, route, rows(value))
		}
		run(fixture.Exec("SET application_name='first'", 0))
		execute(fixture.Exec(query, 0))
		run(rsDatabaseApplication("first"))
		observePeer("first")
		// Self authority succeeds; changing the effective issuer to another
		// ordinary role must not retain the earlier superuser/self capability.
		run(fixture.Exec("SET ROLE rsd_a", 0))
		run(fixture.Exec("SET application_name='self'", 0))
		execute(fixture.Exec(query, 0))
		observePeer("self")
		run(fixture.Exec("SET ROLE rsd_b", 0))
		run(fixture.Exec("SET application_name='denied'", 0))
		execute(fixture.RoleReferenceError(query, "42501", "permission denied to alter role", `Only roles with the CREATEROLE attribute and the ADMIN option on role "rsd_a" may alter this role.`, false, "execute"))
		run(rsDatabaseApplication("denied"))
		observePeer("self")
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET application_name='committed'", 0))
		execute(fixture.Exec(query, 0))
		run(rows("committed"))
		observePeer("self")
		run(fixture.Exec("SAVEPOINT suffix", 0))
		run(fixture.Exec("SET application_name='suffix'", 0))
		execute(fixture.Exec(query, 0))
		run(rows("suffix"))
		run(fixture.Exec("ROLLBACK TO suffix", 0))
		run(rows("committed"))
		run(rsDatabaseApplication("committed"))
		run(fixture.Exec("RELEASE suffix", 0))
		run(fixture.Exec("COMMIT", 0))
		observePeer("committed")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET application_name='rolledback'", 0))
		execute(fixture.Exec(query, 0))
		run(rows("rolledback"))
		observePeer("committed")
		run(fixture.Exec("ROLLBACK", 0))
		observePeer("committed")
		// Reuse after rollback, then fail a suffix in a new transaction: neither
		// the successful private prefix nor any unrelated scope may publish.
		run(fixture.Exec("SET application_name='after'", 0))
		execute(fixture.Exec(query, 0))
		observePeer("after")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET application_name='unpublished'", 0))
		execute(fixture.Exec(query, 0))
		run(fixture.RoleReferenceError("ALTER ROLE rsd_a IN DATABASE rsd_missing SET application_name='bad'", "3D000", "database \"rsd_missing\" does not exist", "", false, "execute"))
		run(fixture.Exec("ROLLBACK", 0))
		observePeer("after")
		for _, cell := range []struct{ database, role, value string }{
			{"rsd_one", "rsd_a", "database"}, {"rsd_two", "rsd_a", "after"}, {"rsd_two", "rsd_b", "other"},
		} {
			fresh, raw := rsDatabasePool(t, p, b, cell.database, cell.role)
			rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(cell.value))
			if err := fresh.DB.Close(); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
