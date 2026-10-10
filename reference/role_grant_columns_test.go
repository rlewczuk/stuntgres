package reference_test

import (
	"database/sql"
	"testing"
)

// Each route starts from an independent catalog. Expectations precede backend
// comparison; utilities and consumers both traverse the actual public driver.
func roleGrantScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, func(referenceStep), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			roleReferenceCleanup(t, []string{"rg_reader", "rg_owner", "rg_a", "rg_b", "rg_c", "rg_group", "rg_future"})
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					conn, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s referenceStep) {
						s.prepared = prepared
						if !t.Run(s.query, func(t *testing.T) {
							referenceRunStep(t, p.ctx, conn, backend.recorder, s)
						}) {
							t.FailNow()
						}
					}
					if backend.pg {
						run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
					}
					for _, q := range []string{"CREATE ROLE rg_reader", "CREATE ROLE rg_owner", "CREATE ROLE rg_a", "CREATE ROLE rg_b", "CREATE ROLE rg_c", "CREATE ROLE rg_group"} {
						run(referenceExec(q, 0))
					}
					body(t, p, backend, conn, run, prepared)
				})
			}
		})
	}
}

func roleGrantDenied(q, object string) referenceStep {
	return referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + object})
}

func TestReferenceRoleGrantColumns(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT,b INT DEFAULT 9)", 0))
		run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
		run(referenceExec("GRANT SELECT(a),INSERT(a),UPDATE(a) ON TABLE t TO GROUP rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		for _, q := range []string{"SELECT * FROM t", "SELECT b FROM t WHERE FALSE", "SELECT a FROM t WHERE b=9", "SELECT a FROM t ORDER BY b", "INSERT INTO t(b) VALUES(1)", "UPDATE t SET a=b", "UPDATE t SET a=8 WHERE b=9", "UPDATE t SET a=8 RETURNING b"} {
			run(roleGrantDenied(q, "t"))
		}
		run(referenceExec("INSERT INTO t(a) VALUES(8)", 1))
		run(referenceExec("UPDATE t SET a=6", 2))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT a,b FROM t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(6), int64(9)}, {int64(6), int64(9)}}))
		run(referenceExec("GRANT DELETE ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("DELETE FROM t WHERE b=9", "t"))
		run(referenceExec("DELETE FROM t", 2))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantRevoke(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
		run(referenceExec("GRANT SELECT,SELECT(a) ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		q := "SELECT a,b FROM t"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		allowed := referenceSelect(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT(a) ON t FROM rg_reader RESTRICT", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, roleGrantDenied(q, "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantOwner(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		stmt, err := c.PrepareContext(p.ctx, "SELECT a FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: "SELECT a FROM t"}
		allowed := referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceExec("REVOKE SELECT ON t FROM rg_owner", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		referenceRunStep(t, p.ctx, held, b.recorder, roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("GRANT SELECT ON t TO rg_reader WITH GRANT OPTION", 0))
		run(referenceExec("GRANT SELECT ON t TO CURRENT_USER", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_c", 0))
		for _, q := range []string{"ALTER TABLE t ADD COLUMN c INT", "DROP TABLE t", "CREATE INDEX ix ON t(a)"} {
			run(referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "must be owner of table t"}))
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantPublicInheritance(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT rg_group TO rg_a WITH INHERIT TRUE", 0))
		run(referenceExec("GRANT rg_group TO rg_b WITH INHERIT FALSE", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_group WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("GRANT SELECT ON t TO rg_c", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT ON t TO PUBLIC", 0))
		run(referenceExec("CREATE ROLE rg_future", 0))
		for _, role := range []string{"rg_b", "rg_future", "rg_c"} {
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}

func TestReferenceRoleGrantInheritedOwner(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT rg_owner TO rg_group WITH INHERIT TRUE", 0))
		run(referenceExec("GRANT rg_group TO rg_a WITH INHERIT TRUE", 0))
		run(referenceExec("GRANT rg_group TO rg_b WITH INHERIT FALSE", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceImplicitError("GRANT SELECT ON t TO rg_reader GRANTED BY rg_owner", referenceDiagnostic{Code: "0A000", Message: "grantor must be current user"}))
		run(referenceExec("GRANT SELECT ON t TO rg_reader GRANTED BY CURRENT_USER", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_owner", 0))
		run(referenceExec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}
