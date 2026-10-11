package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantDelegation(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.ImplicitError("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", fixture.Diagnostic{Code: "0LP01", Message: "grant options cannot be granted back to your own grantor"}))
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.ImplicitError("REVOKE SELECT ON t FROM rg_a RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
		for _, role := range []string{"rg_a", "rg_b", "rg_c"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// An independent owner option source makes the apparent back edge valid.
		run(fixture.Exec("GRANT SELECT ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantSourceProvenance(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT UPDATE ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT rg_a,rg_b TO rg_group WITH INHERIT TRUE", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_group", 0))
		// A single source is chosen. A tie has no portable winning source.
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_group", 0))
		s := fixture.Exec("GRANT SELECT,UPDATE ON t TO rg_reader", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `not all privileges were granted for "t"`}
		if len(notices) != 1 || notices[0] != want {
			t.Fatalf("source warning got %#v want %#v", notices, want)
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Public effects prove it is not a synthesized SELECT|UPDATE grant.
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		rows, se := c.QueryContext(p.Ctx, "SELECT a FROM t")
		if se == nil {
			fixture.DeleteReferenceConsume(t, b.PG, rows, nil, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.RoleGrantDenied("UPDATE t SET a=8", "t"))
		} else {
			fixture.AssertDiagnostic(t, se, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
			run(fixture.Exec("UPDATE t SET a=7", 1))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			run(fixture.Select("SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='t'::regclass AND r.rolname='rg_reader'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
	})
}

func TestReferenceRoleGrantIndependentOrigins(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantColumnDependencies(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Pinned release revocation walks only the table ACL, not the separate
		// column ACL containing rg_a's already-published downstream grant.
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		run(fixture.ImplicitError("GRANT SELECT(a) ON t TO rg_c", fixture.Diagnostic{Code: "42501", Message: `permission denied for column "a" of relation "t"`}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Once support is in the column ACL itself, same-ACL RESTRICT still
		// protects the downstream rg_a grant.
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_b", 0))
		run(fixture.ImplicitError("REVOKE SELECT(a) ON t FROM rg_a RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
