package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesAuthority(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT dp_owner TO dp_member WITH INHERIT TRUE, SET TRUE", 0))
		run(fixture.Exec("GRANT dp_owner TO dp_set WITH INHERIT FALSE, SET TRUE, ADMIN TRUE", 0))
		dpSchema(run, "dp_s", "dp_other")
		dpActor(run, "dp_member", func() {
			// The member neither owns nor has CREATE on dp_s. ALTER itself has no schema gate.
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s GRANT SELECT ON TABLES TO dp_reader", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR USER dp_owner IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		})
		dpActor(run, "dp_set", func() {
			dpError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT INSERT ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
			run(fixture.Exec("SET ROLE dp_owner", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO dp_reader", 0))
			run(fixture.Exec("RESET ROLE", 0))
		})
		dpActor(run, "dp_other", func() {
			dpError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_s GRANT SELECT ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT SELECT ON TABLES TO dp_reader", 0))
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_other IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("GRANT dp_member TO dp_other WITH INHERIT TRUE,SET FALSE", 0))
		dpActor(run, "dp_other", func() {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT UPDATE ON TABLES TO dp_reader", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE UPDATE ON TABLES FROM dp_reader", 0))
		})
		// Mask inheritance, ADMIN, and SET are distinct; disabling INHERIT removes authority.
		run(fixture.Exec("GRANT dp_owner TO dp_member WITH INHERIT FALSE", 0))
		dpActor(run, "dp_member", func() {
			dpError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT UPDATE ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
		})
		dpActor(run, "dp_other", func() {
			dpError(run, "ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT UPDATE ON TABLES TO dp_reader", "42501", "permission denied to change default privileges")
		})
		dpSchema(run, "dp_owner_ns", "dp_owner")
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_auth", 9) })
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_ns.dp_auth", 9)
			run(fixture.Exec("INSERT INTO dp_owner_ns.dp_auth VALUES(11)", 1))
			dpRead(run, "dp_owner_ns.dp_auth", 9, 11)
		})
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_auth' AND grantee='dp_reader'", [][]any{
			{"dp_owner", "dp_reader", "dp_owner_ns", "dp_auth", "INSERT", "NO", "NO"},
			{"dp_owner", "dp_reader", "dp_owner_ns", "dp_auth", "SELECT", "NO", "YES"},
		})
	})
}

func TestReferenceRoleDefaultPrivilegesCurrentRole(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT dp_owner TO dp_member WITH INHERIT TRUE, SET TRUE", 0))
		dpSchema(run, "dp_owner_ns", "dp_owner")
		dpSchema(run, "dp_member_ns", "dp_member")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		dpActor(run, "dp_member", func() {
			dpTable(run, "dp_member_ns.dp_member_table", 7)
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT INSERT ON TABLES TO dp_reader", 0))
			run(fixture.Exec("SET ROLE dp_owner", 0))
			dpTable(run, "dp_owner_ns.dp_owner_table", 9)
			run(fixture.Exec("RESET ROLE", 0))
			// Current-user grantee resolves to the execution actor; SESSION_USER remains member.
			run(fixture.Exec("SET ROLE dp_owner", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO SESSION_USER", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM CURRENT_ROLE", 0))
			run(fixture.Exec("CREATE TABLE dp_owner_ns.dp_symbols(x INT)", 0))
			dpDenied(run, "SELECT x FROM dp_owner_ns.dp_symbols", "table", "dp_symbols")
			run(fixture.Exec("GRANT SELECT ON dp_owner_ns.dp_symbols TO CURRENT_USER", 0))
			dpRead(run, "dp_owner_ns.dp_symbols")
			run(fixture.Exec("RESET ROLE", 0))
		})
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "SELECT x FROM dp_member_ns.dp_member_table", "table", "dp_member_table")
			dpRead(run, "dp_owner_ns.dp_owner_table", 9)
		})
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_owner_table' AND grantee='dp_reader'", [][]any{
			{"dp_owner", "dp_reader", "dp_owner_ns", "dp_owner_table", "INSERT", "NO", "NO"},
			{"dp_owner", "dp_reader", "dp_owner_ns", "dp_owner_table", "SELECT", "NO", "YES"},
		})
	})
}
