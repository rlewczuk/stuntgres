package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRoleGrantSchemaExpansion(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		for _, q := range []string{"CREATE TABLE t(a INT)", "CREATE SEQUENCE s", "CREATE INDEX ix ON t(a)", "CREATE VIEW v AS SELECT a FROM t"} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT SELECT ON ALL TABLES IN SCHEMA public,public TO rg_reader", 0))
		run(referenceExec("CREATE TABLE later(a INT)", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		for _, target := range []string{"t", "v", "m"} {
			run(referenceSelect("SELECT a FROM "+target, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		run(roleGrantDenied("SELECT a FROM later", "later"))
		run(referenceImplicitError("SELECT nextval('s')", referenceDiagnostic{Code: "42501", Message: "permission denied for sequence s"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("CREATE SCHEMA rg_empty", 0))
		run(referenceExec("GRANT ALL ON ALL TABLES IN SCHEMA rg_empty TO rg_reader", 0))
		run(referenceExec("DROP SCHEMA rg_empty", 0))
		run(referenceExec("CREATE SCHEMA rg_owned AUTHORIZATION rg_owner", 0))
		run(referenceExec("CREATE TABLE rg_owned.t(a INT)", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(roleGrantDenied("GRANT SELECT ON ALL TABLES IN SCHEMA rg_owned TO rg_reader", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("DROP SCHEMA rg_owned CASCADE", 0))
	})
}

func TestReferenceRoleGrantKinds(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("CREATE VIEW v AS SELECT a FROM t", 0))
		run(referenceExec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		// REFERENCES/TRIGGER lack native commands, but their real option authority
		// is observable by delegating each selected bit, not pretending to consume it.
		run(referenceExec("GRANT ALL PRIVILEGES ON TABLE t,v,m TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"} {
			run(referenceExec("GRANT "+privilege+" ON t TO rg_reader", 0))
		}
		run(referenceExec("GRANT REFERENCES(a),SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_reader", 0))
		// REL_18_0 checks column-option circularity within the column ACL:
		// table options permit ordinary column delegation, not this option grant.
		run(referenceImplicitError("GRANT ALL(a) ON t TO rg_c WITH GRANT OPTION", referenceDiagnostic{Code: "0LP01", Message: "grant options cannot be granted back to your own grantor"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT ALL(a) ON t TO rg_c WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_c", 0))
		run(referenceExec("GRANT REFERENCES(a),SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_b", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantMaintenance(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("CREATE INDEX ix ON t(a)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT MAINTAIN ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceExec("REINDEX INDEX ix", 0))
		run(referenceExec("REINDEX TABLE t", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE MAINTAIN ON t FROM rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceImplicitError("REINDEX INDEX ix", referenceDiagnostic{Code: "42501", Message: "permission denied for index ix"}))
		run(roleGrantDenied("REINDEX TABLE t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT TRUNCATE ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(referenceExec("ROLLBACK", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE TRUNCATE ON t FROM rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceExec("BEGIN", 0))
		run(roleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "t"))
		run(referenceExec("ROLLBACK", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantSequenceBoundary(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(id SERIAL)", 0))
		run(referenceExec("CREATE TABLE ident(id INT GENERATED ALWAYS AS IDENTITY)", 0))
		run(referenceExec("GRANT ALL ON t,ident TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceImplicitError("INSERT INTO t DEFAULT VALUES", referenceDiagnostic{Code: "42501", Message: "permission denied for sequence t_id_seq"}))
		run(referenceExec("INSERT INTO ident DEFAULT VALUES", 1))
		run(referenceImplicitError("SELECT nextval('ident_id_seq')", referenceDiagnostic{Code: "42501", Message: "permission denied for sequence ident_id_seq"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, nil))
		run(referenceSelect("SELECT last_value,is_called FROM t_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		run(referenceSelect("SELECT id FROM ident", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		for _, q := range []string{"GRANT SELECT ON SEQUENCE t_id_seq TO rg_reader", "GRANT SELECT ON TABLE t_id_seq TO rg_reader", "GRANT SELECT ON t_id_seq TO rg_reader"} {
			if b.pg {
				run(referenceExec(q, 0))
			} else {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is not supported"}))
			}
		}
	})
}
