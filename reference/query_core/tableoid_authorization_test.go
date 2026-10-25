package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"toi_reader"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		routineExec(t, p, b, c, "CREATE ROLE toi_reader", "GRANT USAGE ON SCHEMA public TO toi_reader", "CREATE TABLE toi_acl(a INT)", "CREATE TABLE toi_acl_child() INHERITS(toi_acl)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_acl VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_acl_child VALUES(2)", 1))
		parent := tableoidCell(b, tableoidIdentity(t, p, c, "toi_acl"))
		child := tableoidCell(b, tableoidIdentity(t, p, c, "toi_acl_child"))
		query := "SELECT q.tableoid FROM toi_acl q ORDER BY q.a"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		denied := func(exec fixture.Executor, q, table string) {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, exec, b.Recorder, s)
		}
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION toi_reader")
		denied(c, "SELECT tableoid FROM toi_acl", "toi_acl")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT(a) ON toi_acl TO toi_reader", "SET SESSION AUTHORIZATION toi_reader")
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a FROM toi_acl ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT 1 AS n FROM toi_acl", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}),
		} {
			tableoidRoutes(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT tableoid FROM toi_acl", "SELECT a,tableoid FROM toi_acl"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table toi_acl"})
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		denied(held, query, "toi_acl")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON toi_acl TO toi_reader", "SET SESSION AUTHORIZATION toi_reader")
		want := fixture.Select(query, []string{"tableoid"}, []string{"OID"}, [][]any{{parent}, {child}})
		tableoidRoutes(t, p, b, c, want)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		denied(c, "SELECT tableoid FROM toi_acl_child", "toi_acl_child")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON toi_acl FROM toi_reader", "SET SESSION AUTHORIZATION toi_reader")
		denied(held, query, "toi_acl")
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON toi_acl TO toi_reader", "SET SESSION AUTHORIZATION toi_reader")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	})
}

func TestReferenceQueryTableOIDRLS(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"toi_rls_owner", "toi_rls_reader", "toi_rls_bypass"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		routineExec(t, p, b, c, "CREATE ROLE toi_rls_owner", "CREATE ROLE toi_rls_reader", "CREATE ROLE toi_rls_bypass BYPASSRLS", "GRANT USAGE ON SCHEMA public TO toi_rls_owner,toi_rls_reader,toi_rls_bypass", "CREATE TABLE toi_rls(a INT)", "CREATE TABLE toi_rls_child() INHERITS(toi_rls)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_rls VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_rls_child VALUES(2)", 1))
		parent := tableoidCell(b, tableoidIdentity(t, p, c, "toi_rls"))
		child := tableoidCell(b, tableoidIdentity(t, p, c, "toi_rls_child"))
		routineExec(t, p, b, c, "ALTER TABLE toi_rls OWNER TO toi_rls_owner", "GRANT SELECT ON toi_rls TO toi_rls_reader,toi_rls_bypass", "ALTER TABLE toi_rls ENABLE ROW LEVEL SECURITY")
		q := "SELECT tableoid FROM toi_rls ORDER BY a"
		visible := fixture.Select(q, []string{"tableoid"}, []string{"OID"}, [][]any{{parent}, {child}})
		hidden := fixture.Select(q, []string{"tableoid"}, []string{"OID"}, nil)
		for _, actor := range []struct {
			name    string
			visible bool
		}{{"toi_rls_reader", false}, {"toi_rls_owner", true}, {"toi_rls_bypass", true}} {
			routineExec(t, p, b, c, "SET SESSION AUTHORIZATION "+actor.name)
			s := hidden
			if actor.visible {
				s = visible
			}
			tableoidRoutes(t, p, b, c, s)
			routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		}
		routineExec(t, p, b, c, "ALTER TABLE toi_rls FORCE ROW LEVEL SECURITY", "SET SESSION AUTHORIZATION toi_rls_owner")
		tableoidRoutes(t, p, b, c, hidden)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION toi_rls_bypass")
		tableoidRoutes(t, p, b, c, visible)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		tableoidRoutes(t, p, b, c, visible)
	})
}
