package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRevokeColumns(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		// Table removal uses original table options to select column origins.
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT(a,b) ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a RESTRICT"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_a", true)
		revokeRead(run, "rg_reader", false)
		// Option-only retained both column uses. Plain table revoke removes
		// corresponding grants even after table authority was privately changed.
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_a", false)
		// Selection-only table+column union must not become a dependency graph.
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0)) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
		revokeActor(run, "rg_reader", func() {
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
		})
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a CASCADE", 0))
		revokeActor(run, "rg_reader", func() { run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})) })
		// Same-column chains, unlike cross-level chains, recurse in that ACL.
		run(fixture.Exec("GRANT SELECT(b) ON t TO rg_b WITH GRANT OPTION", 0))
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT(b) ON t TO rg_c WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_c", func() { run(fixture.Exec("GRANT SELECT(b) ON t TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT(b) ON t FROM rg_b RESTRICT"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(b) ON t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_b", func() { run(fixture.Select("SELECT b FROM t", []string{"b"}, []string{"INT4"}, [][]any{{int64(9)}})) })
		revokeActor(run, "rg_reader", func() {
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
		})
		// Column subtraction cannot negate table use or delegation.
		run(fixture.Exec("GRANT SELECT ON t TO rg_c WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_c WITH GRANT OPTION", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(a) ON t FROM rg_c CASCADE", 0))
		revokeActor(run, "rg_c", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_group", 0)) })
		run(fixture.Exec("REVOKE SELECT(a) ON t FROM rg_c CASCADE", 0))
		revokeRead(run, "rg_c", true)
		// Cover every column bit with literal consumers for INSERT/UPDATE.
		run(fixture.Exec("GRANT INSERT(a,b),UPDATE(a,b),REFERENCES(a,b) ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR INSERT(a,b),UPDATE(a,b),REFERENCES(a,b) ON t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_b", func() {
			run(fixture.Exec("INSERT INTO t(a,b) VALUES(8,10)", 1))
			run(fixture.Exec("UPDATE t SET a=7,b=9", 2))
		})
		run(fixture.Exec("REVOKE INSERT,UPDATE,REFERENCES ON t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_b", func() {
			run(fixture.RoleGrantDenied("INSERT INTO t(a,b) VALUES(8,10)", "t"))
			run(fixture.RoleGrantDenied("UPDATE t SET a=7,b=9", "t"))
		})
	})
}

func TestReferenceRoleRevokeProvenance(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		s := fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_reader CASCADE", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		s = fixture.Exec("REVOKE SELECT ON t FROM rg_reader CASCADE", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		revokeNotices(t, notices, nil)
		revokeRead(run, "rg_reader", true)
		revokeActor(run, "rg_a", func() { run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader GRANTED BY CURRENT_ROLE CASCADE", 0)) })
		revokeRead(run, "rg_reader", false)
		// A unique table source differs from the unique column source. The
		// original requested SELECT bit must reach columns despite table source
		// narrowing, and each column must independently infer its own grantor.
		run(fixture.Exec("GRANT INSERT,UPDATE ON t TO rg_a WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_b WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT rg_a,rg_b TO rg_group WITH INHERIT TRUE", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT INSERT,UPDATE ON t TO rg_c WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT(a),INSERT(a),UPDATE(a) ON t TO rg_c WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_c", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0)) })
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a CASCADE", 0))
		revokeActor(run, "rg_group", func() {
			run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT,INSERT,UPDATE ON t FROM rg_c GRANTED BY CURRENT_USER CASCADE", 0))
		})
		revokeActor(run, "rg_c", func() {
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("UPDATE t SET b=9", 1))
		})
		revokeRead(run, "rg_reader", false)
		// Changing the acting identity to inherited grantor is forbidden by
		// GRANTED BY, even though inferred provenance itself is inherited.
		revokeActor(run, "rg_group", func() {
			run(fixture.ImplicitError("REVOKE SELECT(a) ON t FROM rg_c GRANTED BY rg_b CASCADE", fixture.Diagnostic{Code: "0A000", Message: "grantor must be current user"}))
		})
	})
}
