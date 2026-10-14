package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantFeatureSmoke(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "rg_owner", func() { run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 9)", 0)) })
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		q := "SELECT a,b FROM t"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		want := fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(a) ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied(q, "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_owner", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO CURRENT_USER", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		s := fixture.Exec("GRANT SELECT ON t TO rg_c", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "t"`}
		if len(notices) != 1 || notices[0] != warning {
			t.Fatal("non-option warning", notices, warning)
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Two unequal source masks have a unique best grantor: the larger source
		// provides SELECT+INSERT, not the smaller source's UPDATE option.
		run(fixture.Exec("GRANT SELECT,INSERT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT UPDATE ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT rg_a,rg_b TO rg_group WITH INHERIT TRUE", 0))
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_group", 0))
		notices = nil
		s = fixture.Exec("GRANT SELECT,INSERT,UPDATE ON t TO rg_c", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		warning.Message = `not all privileges were granted for "t"`
		if len(notices) != 1 || notices[0] != warning {
			t.Fatal("single-source warning", notices, warning)
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.RoleGrantDenied("UPDATE t SET a=8", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("REVOKE SELECT,INSERT ON t FROM rg_c", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.RoleGrantDenied("INSERT INTO t(a) VALUES(1)", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE VIEW v AS SELECT a FROM t", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW m AS SELECT 7 AS a", 1))
		run(fixture.Exec("CREATE ROLE rg_future", 0))
		run(fixture.Exec("GRANT SELECT ON ALL TABLES IN SCHEMA public TO rg_future", 0))
		run(fixture.Exec("CREATE TABLE later(a INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_future", 0))
		for _, name := range []string{"t", "v", "m"} {
			run(fixture.Select("SELECT a FROM "+name, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		run(fixture.RoleGrantDenied("SELECT a FROM later", "later"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE TABLE serial_t(id SERIAL)", 0))
		run(fixture.Exec("CREATE TABLE identity_t(id INT GENERATED ALWAYS AS IDENTITY)", 0))
		run(fixture.Exec("GRANT INSERT ON serial_t,identity_t TO rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.ImplicitError("INSERT INTO serial_t DEFAULT VALUES", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence serial_t_id_seq"}))
		run(fixture.Exec("INSERT INTO identity_t DEFAULT VALUES", 1))
		run(fixture.ImplicitError("SELECT nextval('identity_t_id_seq')", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence identity_t_id_seq"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT id FROM serial_t", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT last_value,is_called FROM serial_t_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		run(fixture.Select("SELECT id FROM identity_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("CREATE INDEX ix ON t(a)", 0))
		run(fixture.Exec("GRANT MAINTAIN ON t TO rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Exec("REINDEX INDEX ix", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE MAINTAIN ON t FROM rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.ImplicitError("REINDEX INDEX ix", fixture.Diagnostic{Code: "42501", Message: "permission denied for index ix"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT TRUNCATE ON t TO rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE TRUNCATE ON t FROM rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.RoleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "t"))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.ImplicitError("GRANT SELECT ON later,missing_table TO rg_c", fixture.Diagnostic{Code: "42P01", Message: `relation "missing_table" does not exist`}))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM later", "later"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
