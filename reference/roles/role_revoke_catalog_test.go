package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Catalog grants are cleaned by roleGrantCatalogScenario after held handles close.
// These cases use restricted sources so PUBLIC defaults cannot hide revocation.
func TestReferenceRoleRevokeCatalogAdmission(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		const q = "SELECT rolname FROM pg_catalog.pg_authid WHERE rolname='rg_reader'"
		allowed := fixture.Select(q, []string{"rolname"}, []string{"NAME"}, [][]any{{"rg_reader"}})
		denied := fixture.RoleGrantDenied(q, "pg_authid")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func(want fixture.Step) {
			revokeActor(run, "rg_reader", func() {
				run(want)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			})
		}
		check(denied)
		run(fixture.Exec("GRANT SELECT(rolname) ON pg_catalog.pg_authid TO rg_reader", 0))
		check(allowed)
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_authid TO rg_reader", 0))
		run(fixture.Exec("REVOKE SELECT(rolname) ON pg_catalog.pg_authid FROM rg_reader GRANTED BY CURRENT_USER RESTRICT", 0))
		check(allowed) // Column removal cannot subtract table rights.
		run(fixture.Exec("GRANT SELECT(rolname) ON pg_catalog.pg_authid TO rg_reader", 0))
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_authid FROM rg_reader", 0))
		check(denied) // Table removal also removes the corresponding column grant.
		run(fixture.Exec("GRANT ALL ON pg_catalog.pg_authid TO rg_reader", 0))
		check(allowed)
		run(fixture.Exec("REVOKE ALL PRIVILEGES ON pg_catalog.pg_authid FROM rg_reader CASCADE", 0))
		check(denied)
		// Restore the shared protected catalog even on an early failure.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION; REVOKE SELECT(rolname) ON pg_catalog.pg_authid FROM PUBLIC"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("GRANT SELECT(rolname) ON pg_catalog.pg_authid TO PUBLIC", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(rolname) ON pg_catalog.pg_authid FROM PUBLIC CASCADE", 0))
		check(allowed)
		run(fixture.Exec("REVOKE SELECT(rolname) ON pg_catalog.pg_authid FROM PUBLIC", 0))
		check(denied)
		// A grant on a separate protected view never authorizes its backing source.
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader", 0))
		check(denied)
	})
}

func TestReferenceRoleRevokeProviderCascade(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		const q = "SELECT usename FROM pg_catalog.pg_shadow WHERE usename='rg_reader'"
		// rg_reader is NOLOGIN, so this consumer is empty but still ACL checked.
		allowed := fixture.Select(q, []string{"usename"}, []string{"NAME"}, nil)
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for view pg_shadow"})
		denied.SelectRows, denied.ErrorStage = true, "execute"
		check := func(role string, want fixture.Step) {
			revokeActor(run, role, func() { run(want) })
		}
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_b WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT ON pg_catalog.pg_shadow FROM rg_a RESTRICT"))
		check("rg_reader", allowed)
		// An independent owner-origin grant survives the other chain's cascade.
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_reader", 0))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON pg_catalog.pg_shadow FROM rg_a CASCADE", 0))
		check("rg_a", allowed)
		check("rg_b", denied)
		check("rg_reader", allowed)
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT ON pg_catalog.pg_shadow TO rg_c",
			revokeWarning("01007", `no privileges were granted for "pg_shadow"`))
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check("rg_c", denied)
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_shadow FROM rg_reader", 0))
		check("rg_reader", denied)
		// Column ACL recursion stays separate from the table ACL.
		run(fixture.Exec("GRANT SELECT(usename) ON pg_catalog.pg_shadow TO rg_b WITH GRANT OPTION", 0))
		revokeActor(run, "rg_b", func() { run(fixture.Exec("GRANT SELECT(usename) ON pg_catalog.pg_shadow TO rg_reader", 0)) })
		run(revokeDependency("REVOKE GRANT OPTION FOR SELECT(usename) ON pg_catalog.pg_shadow FROM rg_b"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(usename) ON pg_catalog.pg_shadow FROM rg_b CASCADE", 0))
		check("rg_b", allowed)
		check("rg_reader", denied)
	})
}

func TestReferenceRoleRevokeCatalogAtomicity(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT ON t,pg_catalog.pg_authid TO rg_reader", 0))
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_shadow TO rg_b", 0)) })
		for _, tc := range []struct{ query, code, message string }{
			{"REVOKE SELECT ON t,pg_catalog.pg_authid,rr_catalog_missing FROM rg_reader", "42P01", `relation "rr_catalog_missing" does not exist`},
			{"REVOKE SELECT(a) ON t,pg_catalog.pg_authid FROM rg_reader", "42703", `column "a" of relation "pg_authid" does not exist`},
			{"REVOKE SELECT ON t,pg_catalog.pg_authid FROM rg_reader,rr_catalog_missing_role", "42704", `role "rr_catalog_missing_role" does not exist`},
			{"REVOKE DELETE(rolname) ON pg_catalog.pg_authid FROM rg_reader", "0LP01", "invalid privilege type DELETE for column"},
			{"REVOKE SELECT ON rr_catalog_missing FROM rg_reader GRANTED BY rg_owner", "0A000", "grantor must be current user"},
			{"REVOKE SELECT ON rr_catalog_missing FROM rg_reader GRANTED BY rr_catalog_missing_role", "42704", `role "rr_catalog_missing_role" does not exist`},
		} {
			fixture.RoleGrantError(run, tc.query, tc.code, tc.message)
			revokeRead(run, "rg_reader", true)
			revokeActor(run, "rg_reader", func() {
				run(fixture.Select("SELECT rolname FROM pg_catalog.pg_authid WHERE rolname='rg_reader'", []string{"rolname"}, []string{"NAME"}, [][]any{{"rg_reader"}}))
			})
		}
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		run(revokeDependency("REVOKE SELECT ON t,pg_catalog.pg_shadow FROM rg_a RESTRICT"))
		revokeRead(run, "rg_a", true)
		revokeActor(run, "rg_b", func() {
			run(fixture.Select("SELECT usename FROM pg_catalog.pg_shadow WHERE FALSE", []string{"usename"}, []string{"NAME"}, nil))
		})
	})
}

func TestReferenceRoleRevokeCatalogNotices(t *testing.T) {
	roleGrantCatalogScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, options := range []bool{false, true} {
			grant := "GRANT SELECT ON pg_catalog.pg_shadow TO rg_a"
			if options {
				grant += " WITH GRANT OPTION"
			}
			run(fixture.Exec(grant, 0))
			var notices []fixture.Diagnostic
			_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
			prefix := "no privileges could be revoked"
			if options {
				prefix = "not all privileges could be revoked"
			}
			want := []fixture.Diagnostic{revokeWarning("01006", prefix+` for "pg_shadow"`)}
			// pg_shadow is a view: no negative system attributes are visited.
			for _, column := range []string{"usename", "usesysid", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "valuntil", "useconfig"} {
				want = append(want, revokeWarning("01006", prefix+` for column "`+column+`" of relation "pg_shadow"`))
			}
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE SELECT,UPDATE ON pg_catalog.pg_shadow FROM rg_reader", want...)
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
