package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesSequences(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SEQUENCE dp_seq_old", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE dp_seq_usage START 9", 0))
		run(fixture.Exec("CREATE TEMP SEQUENCE dp_seq_temp START 7", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('dp_seq_old')", "sequence", "dp_seq_old")
			run(fixture.Select("SELECT nextval('dp_seq_usage')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT currval('dp_seq_usage')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(9)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT setval('dp_seq_usage',20)", "sequence", "dp_seq_usage")
			fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM dp_seq_usage", "sequence", "dp_seq_usage")
			run(fixture.Select("SELECT nextval('dp_seq_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(7)}}))
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON SEQUENCES FROM dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE dp_seq_select START 11", 0))
		run(fixture.Select("SELECT nextval('dp_seq_select')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(11)}}))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			run(fixture.Select("SELECT last_value FROM dp_seq_select", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(11)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('dp_seq_select')", "sequence", "dp_seq_select")
			fixture.RoleGrantPermissionDenied(run, "SELECT setval('dp_seq_select',20)", "sequence", "dp_seq_select")
			run(fixture.Select("SELECT currval('dp_seq_select')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(11)}}))
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON SEQUENCES FROM dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT UPDATE ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE dp_seq_update START 13", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			run(fixture.Select("SELECT setval('dp_seq_update',19)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(19)}}))
			run(fixture.Select("SELECT nextval('dp_seq_update')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT currval('dp_seq_update')", "sequence", "dp_seq_update")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE UPDATE ON SEQUENCES FROM dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_serial_old(id SERIAL,x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO dp_serial_old(x) VALUES(7)", "sequence", "dp_serial_old_id_seq")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_serial_new(id SERIAL,x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			run(fixture.Exec("INSERT INTO dp_serial_new(x) VALUES(9)", 1))
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO dp_serial_old(x) VALUES(11)", "sequence", "dp_serial_old_id_seq")
		})
		run(fixture.Select("SELECT id,x FROM dp_serial_new", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		run(fixture.Select("SELECT id,x FROM dp_serial_old", []string{"id", "x"}, []string{"INT4", "INT4"}, nil))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON SEQUENCES FROM dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_identity_old(id INT GENERATED ALWAYS AS IDENTITY,x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			run(fixture.Exec("INSERT INTO dp_identity_old(x) VALUES(7)", 1))
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('dp_identity_old_id_seq')", "sequence", "dp_identity_old_id_seq")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON SEQUENCES TO dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_identity_new(id INT GENERATED ALWAYS AS IDENTITY,x INT)", 0))
		fixture.RoleGrantActor(run, "dp_reader", func() {
			run(fixture.Exec("INSERT INTO dp_identity_new(x) VALUES(9)", 1))
			run(fixture.Select("SELECT nextval('dp_identity_new_id_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
		run(fixture.Select("SELECT id,x FROM dp_identity_old", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
		run(fixture.Select("SELECT id,x FROM dp_identity_new", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
	})
}
