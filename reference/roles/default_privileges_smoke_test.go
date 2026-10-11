package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesFeatureSmoke(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		owner, _ := dpEnvironment(t, p, c)
		dpTable(run, "dp_old", 7)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_new", 9)
		dpActor(run, "dp_reader", func() { dpDenied(run, "SELECT x FROM dp_old", "table", "dp_old"); dpRead(run, "dp_new", 9) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_next", 11)
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_new", 9); dpDenied(run, "SELECT x FROM dp_next", "table", "dp_next") })
		dpSchema(run, "dp_s", "dp_reader")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT INSERT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_s.dp_scoped", 9)
		dpActor(run, "dp_reader", func() {
			run(fixture.Exec("INSERT INTO dp_s.dp_scoped VALUES(11)", 1))
			dpRead(run, "dp_s.dp_scoped", 9, 11)
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE INSERT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_s.dp_scoped_next", 13)
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_s.dp_scoped_next", 13)
			dpDenied(run, "INSERT INTO dp_s.dp_scoped_next VALUES(17)", "table", "dp_scoped_next")
			run(fixture.Exec("INSERT INTO dp_s.dp_scoped VALUES(17)", 1))
		})
		run(fixture.Exec("GRANT dp_owner TO dp_member WITH INHERIT TRUE,SET TRUE", 0))
		dpSchema(run, "dp_owner_ns", "dp_owner")
		dpSchema(run, "dp_member_ns", "dp_member")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		dpActor(run, "dp_member", func() {
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
			dpTable(run, "dp_member_ns.dp_member_table", 7)
			run(fixture.Exec("SET ROLE dp_owner", 0))
			dpTable(run, "dp_owner_ns.dp_owner_table", 9)
			run(fixture.Exec("RESET ROLE", 0))
		})
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "SELECT x FROM dp_member_ns.dp_member_table", "table", "dp_member_table")
			dpRead(run, "dp_owner_ns.dp_owner_table", 9)
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_serial(id SERIAL,x INT)", 0))
		dpActor(run, "dp_reader", func() { dpDenied(run, "INSERT INTO dp_serial(x) VALUES(7)", "sequence", "dp_serial_id_seq") })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_serial_next(id SERIAL,x INT)", 0))
		dpActor(run, "dp_reader", func() {
			run(fixture.Exec("INSERT INTO dp_serial_next(x) VALUES(9)", 1))
			dpDenied(run, "INSERT INTO dp_serial(x) VALUES(11)", "sequence", "dp_serial_id_seq")
		})
		run(fixture.Select("SELECT id,x FROM dp_serial_next", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		dpProjection(t, p, c, run, "table_schema='dp_s' AND table_name='dp_scoped' AND grantee='dp_reader'", [][]any{
			{owner, "dp_reader", "dp_s", "dp_scoped", "INSERT", "NO", "NO"},
			{owner, "dp_reader", "dp_s", "dp_scoped", "SELECT", "NO", "YES"},
		})
		dpActor(run, "dp_reader", func() {
			dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_serial_next' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_serial_next", "INSERT", "NO", "NO"}, {owner, "dp_reader", "public", "dp_serial_next", "SELECT", "NO", "YES"}})
			dpProjection(t, p, c, run, "table_schema='public' AND (table_name='dp_serial_id_seq' OR table_name='dp_serial_next_id_seq')", nil)
		})
	})
}
