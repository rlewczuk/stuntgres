package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesPrecedence(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_s", "dp_reader")
		dpSchema(run, "dp_t", "dp_reader")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s,dp_s GRANT INSERT ON TABLES TO dp_reader,dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_s.dp_both", 9)
		dpTable(run, "dp_t.dp_global", 7)
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_s.dp_both", 9)
			run(fixture.Exec("INSERT INTO dp_s.dp_both VALUES(11)", 1))
			dpRead(run, "dp_s.dp_both", 9, 11)
			dpRead(run, "dp_t.dp_global", 7)
			dpDenied(run, "INSERT INTO dp_t.dp_global VALUES(11)", "table", "dp_global")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE INSERT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_s.dp_scoped_next", 13)
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_s.dp_scoped_next", 13)
			dpDenied(run, "INSERT INTO dp_s.dp_scoped_next VALUES(17)", "table", "dp_scoped_next")
			run(fixture.Exec("INSERT INTO dp_s.dp_both VALUES(17)", 1))
			dpRead(run, "dp_s.dp_both", 9, 11, 17)
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_s.dp_none", 19)
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "SELECT x FROM dp_s.dp_none", "table", "dp_none")
			dpRead(run, "dp_t.dp_global", 7)
		})
		// Multiple creators and schemas are one statement; duplicate identities are legal.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner,dp_member,dp_owner IN SCHEMA dp_s,dp_t,dp_s GRANT SELECT ON TABLES TO dp_reader", 0))
		// Creator cannot create in reader's schemas; use creator-owned namespaces to witness each role.
		dpSchema(run, "dp_owner_ns", "dp_owner")
		dpSchema(run, "dp_member_ns", "dp_member")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner,dp_member IN SCHEMA dp_owner_ns,dp_member_ns GRANT SELECT ON TABLES TO dp_reader", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_multi_owner", 23) })
		dpActor(run, "dp_member", func() { dpTable(run, "dp_member_ns.dp_multi_member", 29) })
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_owner_ns.dp_multi_owner", 23); dpRead(run, "dp_member_ns.dp_multi_member", 29) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_builtin AS ENUM('a')", 0))
		dpActor(run, "dp_reader", func() { run(fixture.Exec("CREATE TABLE dp_s.dp_builtin_use(x dp_s.dp_builtin)", 0)) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT USAGE ON TYPES TO PUBLIC", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_restored AS ENUM('b')", 0))
		run(fixture.Exec("CREATE TYPE dp_t.dp_denied AS ENUM('c')", 0))
		dpActor(run, "dp_reader", func() {
			run(fixture.Exec("CREATE TABLE dp_s.dp_restored_use(x dp_s.dp_restored)", 0))
			dpDenied(run, "CREATE TABLE dp_t.dp_denied_use(x dp_t.dp_denied)", "type", "dp_t.dp_denied")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		run(fixture.Exec("CREATE TYPE dp_s.dp_after_revoke AS ENUM('d')", 0))
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "CREATE TABLE dp_s.dp_after_use(x dp_s.dp_after_revoke)", "type", "dp_s.dp_after_revoke")
			run(fixture.Exec("CREATE TABLE dp_s.dp_still_use(x dp_s.dp_restored)", 0))
		})
	})
}
