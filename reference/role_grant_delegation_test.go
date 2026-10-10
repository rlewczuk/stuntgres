package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRoleGrantDelegation(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_b WITH GRANT OPTION", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(referenceImplicitError("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", referenceDiagnostic{Code: "0LP01", Message: "grant options cannot be granted back to your own grantor"}))
		run(referenceExec("GRANT SELECT ON t TO rg_c", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceImplicitError("REVOKE SELECT ON t FROM rg_a RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
		for _, role := range []string{"rg_a", "rg_b", "rg_c"} {
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		}
		// An independent owner option source makes the apparent back edge valid.
		run(referenceExec("GRANT SELECT ON t TO rg_b WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantSourceProvenance(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("GRANT UPDATE ON t TO rg_b WITH GRANT OPTION", 0))
		run(referenceExec("GRANT rg_a,rg_b TO rg_group WITH INHERIT TRUE", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_group", 0))
		// A single source is chosen. A tie has no portable winning source.
		var notices []referenceDiagnostic
		_, nc, recorder := noticeReferencePool(t, p, b.pg, &notices)
		referenceRunStep(t, p.ctx, nc, recorder, referenceExec("SET SESSION AUTHORIZATION rg_group", 0))
		s := referenceExec("GRANT SELECT,UPDATE ON t TO rg_reader", 0)
		s.prepared = prepared
		referenceRunStep(t, p.ctx, nc, recorder, s)
		want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `not all privileges were granted for "t"`}
		if len(notices) != 1 || notices[0] != want {
			t.Fatalf("source warning got %#v want %#v", notices, want)
		}
		referenceRunStep(t, p.ctx, nc, recorder, referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Public effects prove it is not a synthesized SELECT|UPDATE grant.
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		rows, se := c.QueryContext(p.ctx, "SELECT a FROM t")
		if se == nil {
			deleteReferenceConsume(t, b.pg, rows, nil, referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(roleGrantDenied("UPDATE t SET a=8", "t"))
		} else {
			referenceAssertDiagnostic(t, se, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
			run(referenceExec("UPDATE t SET a=7", 1))
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		if b.pg {
			run(referenceSelect("SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='t'::regclass AND r.rolname='rg_reader'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
	})
}

func TestReferenceRoleGrantIndependentOrigins(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceExec("GRANT SELECT ON t TO rg_reader", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceExec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceRoleGrantColumnDependencies(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
		run(referenceExec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(referenceExec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Pinned release revocation walks only the table ACL, not the separate
		// column ACL containing rg_a's already-published downstream grant.
		run(referenceExec("REVOKE SELECT ON t FROM rg_a", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(roleGrantDenied("SELECT b FROM t", "t"))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_a", 0))
		run(roleGrantDenied("SELECT a FROM t", "t"))
		run(referenceImplicitError("GRANT SELECT(a) ON t TO rg_c", referenceDiagnostic{Code: "42501", Message: `permission denied for column "a" of relation "t"`}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		// Once support is in the column ACL itself, same-ACL RESTRICT still
		// protects the downstream rg_a grant.
		run(referenceExec("GRANT SELECT(a) ON t TO rg_b WITH GRANT OPTION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(referenceExec("GRANT SELECT(a) ON t TO rg_a WITH GRANT OPTION", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("SET SESSION AUTHORIZATION rg_b", 0))
		run(referenceImplicitError("REVOKE SELECT(a) ON t FROM rg_a RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
}
