package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantSchemaExpansion(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, q := range []string{"CREATE TABLE t(a INT)", "CREATE SEQUENCE s", "CREATE INDEX ix ON t(a)", "CREATE VIEW v AS SELECT a FROM t"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON ALL TABLES IN SCHEMA public,public TO rg_reader", 0))
		run(fixture.Exec("CREATE TABLE later(a INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		for _, target := range []string{"t", "v", "m"} {
			run(fixture.Select("SELECT a FROM "+target, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		run(fixture.RoleGrantDenied("SELECT a FROM later", "later"))
		run(fixture.ImplicitError("SELECT nextval('s')", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence s"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE SCHEMA rg_empty", 0))
		run(fixture.Exec("GRANT ALL ON ALL TABLES IN SCHEMA rg_empty TO rg_reader", 0))
		run(fixture.Exec("DROP SCHEMA rg_empty", 0))
		run(fixture.Exec("CREATE SCHEMA rg_owned AUTHORIZATION rg_owner", 0))
		run(fixture.Exec("CREATE TABLE rg_owned.t(a INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(fixture.RoleGrantDenied("GRANT SELECT ON ALL TABLES IN SCHEMA rg_owned TO rg_reader", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("DROP SCHEMA rg_owned CASCADE", 0))
	})
}

func TestReferenceRoleGrantKinds(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("CREATE VIEW v AS SELECT a FROM t", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		// Delegation exercises the selected privilege bits independently of their
		// command consumers; FK declaration authority has separate shared coverage.
		run(fixture.Exec("GRANT ALL PRIVILEGES ON TABLE t,v,m TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"} {
			run(fixture.Exec("GRANT "+privilege+" ON t TO rg_reader", 0))
		}
		run(fixture.Exec("GRANT REFERENCES(a),SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_reader", 0))
		// REL_18_0 checks column-option circularity within the column ACL:
		// table options permit ordinary column delegation, not this option grant.
		run(fixture.ImplicitError("GRANT ALL(a) ON t TO rg_c WITH GRANT OPTION", fixture.Diagnostic{Code: "0LP01", Message: "grant options cannot be granted back to your own grantor"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT ALL(a) ON t TO rg_c WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Exec("GRANT REFERENCES(a),SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_b", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantMaintenance(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("CREATE INDEX ix ON t(a)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT MAINTAIN ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("REINDEX INDEX ix", 0))
		run(fixture.Exec("REINDEX TABLE t", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE MAINTAIN ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.ImplicitError("REINDEX INDEX ix", fixture.Diagnostic{Code: "42501", Message: "permission denied for index ix"}))
		run(fixture.RoleGrantDenied("REINDEX TABLE t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT TRUNCATE ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE TRUNCATE ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.RoleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "t"))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantSequenceBoundary(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(id SERIAL)", 0))
		run(fixture.Exec("CREATE TABLE ident(id INT GENERATED ALWAYS AS IDENTITY)", 0))
		run(fixture.Exec("GRANT ALL ON t,ident TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.ImplicitError("INSERT INTO t DEFAULT VALUES", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence t_id_seq"}))
		run(fixture.Exec("INSERT INTO ident DEFAULT VALUES", 1))
		run(fixture.ImplicitError("SELECT nextval('ident_id_seq')", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence ident_id_seq"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT last_value,is_called FROM t_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		run(fixture.Select("SELECT id FROM ident", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		for _, q := range []string{"GRANT SELECT ON SEQUENCE t_id_seq TO rg_reader", "GRANT SELECT ON TABLE t_id_seq TO rg_reader", "GRANT SELECT ON t_id_seq TO rg_reader"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT last_value FROM t_id_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.ImplicitError("SELECT nextval('t_id_seq')", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence t_id_seq"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
