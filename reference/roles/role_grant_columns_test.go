package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantColumns(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 9)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		run(fixture.Exec("GRANT SELECT(a),INSERT(a),UPDATE(a) ON TABLE t TO GROUP rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		for _, q := range []string{"SELECT * FROM t", "SELECT b FROM t WHERE FALSE", "SELECT a FROM t WHERE b=9", "SELECT a FROM t ORDER BY b", "INSERT INTO t(b) VALUES(1)", "UPDATE t SET a=b", "UPDATE t SET a=8 WHERE b=9", "UPDATE t SET a=8 RETURNING b"} {
			run(fixture.RoleGrantDenied(q, "t"))
		}
		run(fixture.Exec("INSERT INTO t(a) VALUES(8)", 1))
		run(fixture.Exec("UPDATE t SET a=6", 2))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT a,b FROM t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(6), int64(9)}, {int64(6), int64(9)}}))
		run(fixture.Exec("GRANT DELETE ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("DELETE FROM t WHERE b=9", "t"))
		run(fixture.Exec("DELETE FROM t", 2))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantRevoke(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		run(fixture.Exec("GRANT SELECT,SELECT(a) ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		q := "SELECT a,b FROM t"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		allowed := fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(a) ON t FROM rg_reader RESTRICT", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied(q, "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantOwner(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "rg_owner", func() { run(fixture.Exec("CREATE TABLE t(a INT)", 0)) })
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		stmt, err := c.PrepareContext(p.Ctx, "SELECT a FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT a FROM t"}
		allowed := fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_owner", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO CURRENT_USER", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		for _, q := range []string{"ALTER TABLE t ADD COLUMN c INT", "DROP TABLE t", "CREATE INDEX ix ON t(a)"} {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantPublicInheritance(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT rg_group TO rg_a WITH INHERIT TRUE", 0))
		run(fixture.Exec("GRANT rg_group TO rg_b WITH INHERIT FALSE", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_group WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO PUBLIC", 0))
		run(fixture.Exec("CREATE ROLE rg_future", 0))
		for _, role := range []string{"rg_b", "rg_future", "rg_c"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}

func TestReferenceRoleGrantInheritedOwner(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "rg_owner", func() { run(fixture.Exec("CREATE TABLE t(a INT)", 0)) })
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT rg_owner TO rg_group WITH INHERIT TRUE", 0))
		run(fixture.Exec("GRANT rg_group TO rg_a WITH INHERIT TRUE", 0))
		run(fixture.Exec("GRANT rg_group TO rg_b WITH INHERIT FALSE", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.ImplicitError("GRANT SELECT ON t TO rg_reader GRANTED BY rg_owner", fixture.Diagnostic{Code: "0A000", Message: "grantor must be current user"}))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader GRANTED BY CURRENT_USER", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
