package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesSavepoints(t *testing.T) {
	transactionalPrivilegesOwner(t)
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, base func(fixture.Step), prepared bool) {
		base(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		base(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "GRANT SELECT(b) ON t TO rg_reader"
		utility, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer utility.Close()
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader", 0))
		run(fixture.Exec("SAVEPOINT acl_mark", 0))
		source := fixture.Executor(c)
		if prepared {
			source = fixture.HeldStatement{Executor: c, Statement: utility, SQL: q}
		}
		fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(q, 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ROLLBACK TO acl_mark", 0))
		run(fixture.Exec("ROLLBACK TO acl_mark", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		denied := fixture.RoleGrantDenied("SELECT b FROM t", "t")
		denied.SelectRows = true
		run(denied)
		run(fixture.ImplicitError("GRANT SELECT ON t TO rg_b", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO acl_mark", 0))
		// SET ROLE was postmark too; rollback restores the issuer before this grant.
		late := "GRANT SELECT ON t,tx_missing TO rg_reader"
		run(fixture.ImplicitError(late, fixture.Diagnostic{Code: "42P01", Message: `relation "tx_missing" does not exist`}))
		run(fixture.Exec("ROLLBACK TO acl_mark", 0))
		run(fixture.Exec("RELEASE acl_mark", 0))
		run(fixture.Exec("COMMIT", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET ROLE rg_reader", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT release_mark", 0))
		run(fixture.Exec(q, 0))
		run(fixture.Exec("RELEASE release_mark", 0))
		run(fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET ROLE rg_reader", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceTransactionalPrivilegesDelegation(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
		for _, priv := range []string{"SELECT", "SELECT(a)"} {
			run(fixture.Exec("GRANT "+priv+" ON t TO rg_a WITH GRANT OPTION", 0))
			fixture.RoleGrantActor(run, "rg_a", func() { run(fixture.Exec("GRANT "+priv+" ON t TO rg_b", 0)) })
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("SAVEPOINT restrict_mark", 0))
			for _, restrict := range []string{"", " RESTRICT"} {
				q := "REVOKE GRANT OPTION FOR " + priv + " ON t FROM rg_a" + restrict
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
				run(fixture.Exec("ROLLBACK TO restrict_mark", 0))
			}
			run(fixture.Exec("SET ROLE rg_b", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE GRANT OPTION FOR "+priv+" ON t FROM rg_a CASCADE", 0))
			run(fixture.Exec("SET ROLE rg_a", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SAVEPOINT cascade_probe", 0))
			run(fixture.Exec("SET ROLE rg_b", 0))
			run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
			run(fixture.Exec("ROLLBACK TO cascade_probe", 0))
			run(fixture.Exec("ROLLBACK", 0))
			// An independently owned direct path survives removal of a-origin support.
			run(fixture.Exec("GRANT "+priv+" ON t TO rg_b", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("REVOKE GRANT OPTION FOR "+priv+" ON t FROM rg_a CASCADE", 0))
			run(fixture.Exec("COMMIT", 0))
			fixture.RoleGrantActor(run, "rg_b", func() { run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})) })
			run(fixture.Exec("REVOKE ALL ON t FROM rg_a,rg_b CASCADE", 0))
			run(fixture.Exec("REVOKE ALL(a) ON t FROM rg_a,rg_b CASCADE", 0))
		}
	})
}
