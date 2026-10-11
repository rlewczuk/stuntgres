package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func revokeChain(run func(fixture.Step)) {
	run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
	revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_b WITH GRANT OPTION", 0)) })
	revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0)) })
}

func TestReferenceRoleRevokeCascade(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		for _, optionOnly := range []bool{true, false} {
			revokeChain(run)
			prefix := "REVOKE SELECT"
			if optionOnly {
				prefix = "REVOKE GRANT OPTION FOR SELECT"
			}
			for _, suffix := range []string{"", " RESTRICT"} {
				run(revokeDependency(prefix + " ON t FROM rg_a" + suffix))
				for _, role := range []string{"rg_a", "rg_b", "rg_c"} {
					revokeRead(run, role, true)
				}
			}
			run(fixture.Exec(prefix+" ON t FROM rg_a CASCADE", 0))
			revokeRead(run, "rg_a", optionOnly)
			revokeRead(run, "rg_b", false)
			revokeRead(run, "rg_c", false)
			// Regrant demonstrates that dependent ACL rows were removed, not
			// merely hidden by an upstream effective-access check.
			run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
			revokeRead(run, "rg_b", false)
			revokeRead(run, "rg_c", false)
		}
		// Successful removal prunes both recipient and downstream grantor
		// references; DROP ROLE is the public dependency-store witness.
		run(fixture.Exec("CREATE ROLE rg_future", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_future WITH GRANT OPTION", 0))
		revokeActor(run, "rg_future", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_future CASCADE", 0))
		run(fixture.Exec("DROP ROLE rg_future", 0))
		revokeRead(run, "rg_reader", false)
	})
}

func TestReferenceRoleRevokeAlternateSupport(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		// Four existing fixture roles supply the diamond without extra globals:
		// owner -> a,b -> c -> reader.
		for _, ordinary := range []bool{false, true} {
			run(fixture.Exec("GRANT SELECT ON t TO rg_a,rg_b WITH GRANT OPTION", 0))
			revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_c WITH GRANT OPTION", 0)) })
			revokeActor(run, "rg_b", func() {
				q := "GRANT SELECT ON t TO rg_c WITH GRANT OPTION"
				if ordinary {
					q = "GRANT SELECT ON t TO rg_c"
				}
				run(fixture.Exec(q, 0))
			})
			revokeActor(run, "rg_c", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
			revokeActor(run, "rg_a", func() { run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_c CASCADE", 0)) })
			revokeRead(run, "rg_c", true)
			revokeRead(run, "rg_reader", !ordinary)
			if !ordinary {
				revokeActor(run, "rg_b", func() {
					run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_c RESTRICT"))
					run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_c CASCADE", 0))
				})
				revokeRead(run, "rg_c", true)
				revokeRead(run, "rg_reader", false)
			}
			for _, role := range []string{"rg_a", "rg_b"} {
				revokeActor(run, role, func() { run(fixture.Exec("REVOKE SELECT ON t FROM rg_c CASCADE", 0)) })
			}
		}
		// Independent inherited grant options protect the recipient's own
		// origin; a NOINHERIT edge does not substitute for option support.
		for _, inherit := range []string{"TRUE", "FALSE"} {
			run(fixture.Exec("GRANT SELECT ON t TO rg_a,rg_group WITH GRANT OPTION", 0))
			run(fixture.Exec("GRANT rg_group TO rg_a WITH INHERIT "+inherit, 0))
			revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
			run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
			revokeRead(run, "rg_reader", inherit == "TRUE")
			// Remove the surviving attributed row while inherited authority is
			// still available, before isolating the next membership variant.
			revokeActor(run, "rg_a", func() { run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader CASCADE", 0)) })
			run(fixture.Exec("REVOKE rg_group FROM rg_a", 0))
			run(fixture.Exec("REVOKE SELECT ON t FROM rg_group CASCADE", 0))
		}
	})
}

func TestReferenceRoleRevokeSelectiveBits(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT,UPDATE ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT,UPDATE ON t TO rg_b WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT,UPDATE ON t TO rg_c", 0)) })
		// Unrelated owner-origin SELECT survives the attributed recursive loss.
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_a", true)
		revokeRead(run, "rg_b", false)
		revokeRead(run, "rg_c", true)
		revokeActor(run, "rg_b", func() {
			run(fixture.Exec("UPDATE t SET b=9", 1))
			run(fixture.Exec("GRANT UPDATE ON t TO rg_reader", 0))
		})
		revokeActor(run, "rg_reader", func() {
			run(fixture.Exec("UPDATE t SET b=9", 1))
			run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR UPDATE ON t FROM rg_a CASCADE", 0))
		revokeActor(run, "rg_b", func() { run(fixture.RoleGrantDenied("UPDATE t SET b=9", "t")) })
		revokeActor(run, "rg_c", func() { run(fixture.RoleGrantDenied("UPDATE t SET b=9", "t")) })
		revokeRead(run, "rg_c", true)
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_c", 0))
		revokeRead(run, "rg_c", false)
	})
}

func TestReferenceRoleRevokeIndependentAccess(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader,rg_group", 0))
		run(fixture.Exec("GRANT rg_group TO rg_reader WITH INHERIT TRUE", 0))
		run(fixture.Exec("GRANT SELECT ON t TO PUBLIC", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		revokeRead(run, "rg_reader", true)
		run(fixture.Exec("REVOKE SELECT ON t FROM PUBLIC", 0))
		revokeRead(run, "rg_reader", true)
		run(fixture.Exec("GRANT rg_group TO rg_reader WITH INHERIT FALSE", 0))
		revokeRead(run, "rg_reader", false)
		revokeActor(run, "rg_reader", func() {
			run(fixture.Exec("SET ROLE rg_group", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("RESET ROLE", 0))
		})
		// Independent direct and PUBLIC use survives a dependency cascade.
		revokeChain(run)
		run(fixture.Exec("GRANT SELECT ON t TO rg_c", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_a CASCADE", 0))
		revokeRead(run, "rg_c", true)
		run(fixture.Exec("GRANT SELECT ON t TO PUBLIC", 0))
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_c", 0))
		revokeRead(run, "rg_c", true)
		run(fixture.Exec("REVOKE SELECT ON t FROM PUBLIC", 0))
		revokeRead(run, "rg_c", false)
	})
}
