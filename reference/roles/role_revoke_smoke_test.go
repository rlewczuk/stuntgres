package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRevokeFeatureSmoke(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeActor(run, "rg_owner", func() { revokeTable(run) })
		revokeChain(run)
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
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
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a RESTRICT"))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_a", true)
		revokeRead(run, "rg_b", false)
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied(q, "t"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		s := fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		revokeNotices(t, notices, []fixture.Diagnostic{revokeWarning("01007", `no privileges were granted for "t"`)})
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		revokeRead(run, "rg_reader", false)
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_c", true)
		run(fixture.Exec("GRANT SELECT ON t TO PUBLIC", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM PUBLIC CASCADE", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_c", 0))
		revokeRead(run, "rg_c", true)
		run(fixture.Exec("REVOKE SELECT ON t FROM PUBLIC", 0))
		revokeRead(run, "rg_c", false)
		// Cross-level downstream column provenance is not a table dependency.
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0)) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
		revokeActor(run, "rg_reader", func() { run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})) })
		// A same-column dependency does cascade and leaves upstream use intact.
		run(fixture.Exec("GRANT SELECT(b) ON t TO rg_b WITH GRANT OPTION", 0))
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT(b) ON t TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT(b) ON t FROM rg_b RESTRICT"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(b) ON t FROM rg_b CASCADE", 0))
		revokeActor(run, "rg_reader", func() {
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.RoleGrantDenied("SELECT b FROM t", "t"))
		})
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_c", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
