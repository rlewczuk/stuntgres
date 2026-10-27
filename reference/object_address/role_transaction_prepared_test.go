package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalPrepared(t *testing.T) {
	for _, mode := range []string{"autocommit", "sql", "go"} {
		for _, inside := range []bool{false, true} {
			label := "outside"
			if inside {
				label = "inside"
			}
			t.Run(mode+"/"+label, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"rq_new", "rq_target", "rq_drop", "rq_member", "rq_revoke"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
						run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, record, s) }
						bootstrap := "postgres"
						if b.PG {
							bootstrap = "stuntgres"
						}
						for _, q := range []string{"CREATE ROLE rq_target", "CREATE ROLE rq_drop", "CREATE ROLE rq_member LOGIN", "CREATE ROLE rq_revoke", "GRANT rq_target TO rq_revoke"} {
							run(c, fixture.Exec(q, 0))
						}
						queries := []string{"CREATE ROLE rq_new", "ALTER ROLE rq_target LOGIN", "DROP ROLE rq_drop", "GRANT rq_target TO rq_member", "REVOKE rq_target FROM rq_revoke", "SET SESSION AUTHORIZATION rq_member", "RESET SESSION AUTHORIZATION"}
						var e fixture.Executor = c
						finish := func(bool) {}
						if inside {
							e, finish = fixture.RoleTransactionBegin(t, p, b, c, mode)
						}
						stmts := make([]*sql.Stmt, len(queries))
						for i, q := range queries {
							stmt, err := e.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal("quiet Prepare", q, err)
							}
							stmts[i] = stmt
							defer stmt.Close()
						}
						if len(notices) != 0 {
							t.Fatal("Prepare emitted notices", notices)
						}
						quiet := func(e fixture.Executor) {
							run(e, fixture.RoleTransactionExists("rq_new", false))
							run(e, fixture.RoleTransactionExists("rq_drop", true))
							run(e, fixture.Select("SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname='rq_target'", []string{"rolcanlogin"}, []string{"BOOL"}, [][]any{{false}}))
							run(e, fixture.Select("SELECT pg_has_role('rq_member','rq_target','MEMBER') AS member,pg_has_role('rq_revoke','rq_target','MEMBER') AS revoked", []string{"member", "revoked"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
							run(e, fixture.RoleTransactionIdentity(bootstrap, bootstrap))
						}
						quiet(e)
						if !inside {
							e, finish = fixture.RoleTransactionBegin(t, p, b, c, mode)
						}
						execute := func(e fixture.Executor, i int) {
							stmt := stmts[i]
							if tx, ok := e.(*sql.Tx); ok && !inside {
								stmt = tx.StmtContext(p.Ctx, stmt)
								defer stmt.Close()
							}
							run(fixture.HeldStatement{Executor: e, Statement: stmt, SQL: queries[i]}, fixture.Exec(queries[i], 0))
						}
						for i := range queries {
							execute(e, i)
						}
						run(e, fixture.RoleTransactionExists("rq_new", true))
						run(e, fixture.RoleTransactionExists("rq_drop", false))
						run(e, fixture.Select("SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname='rq_target'", []string{"rolcanlogin"}, []string{"BOOL"}, [][]any{{true}}))
						run(e, fixture.Select("SELECT pg_has_role('rq_member','rq_target','MEMBER') AS member,pg_has_role('rq_revoke','rq_target','MEMBER') AS revoked", []string{"member", "revoked"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
						var first string
						queryOID := func(e fixture.Executor) string {
							rows, err := e.QueryContext(p.Ctx, "SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname='rq_new'")
							if err != nil {
								t.Fatal(err)
							}
							defer rows.Close()
							var id string
							if !rows.Next() {
								t.Fatal("missing role identity", rows.Err())
							}
							if err := rows.Scan(&id); err != nil {
								t.Fatal(err)
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal("extra role identity", rows.Err())
							}
							fixture.RoleTransactionValidOID(t, id)
							return id
						}
						first = queryOID(e)
						if mode == "autocommit" {
							finish(true)
							return
						}
						finish(false)
						quiet(c)
						if inside {
							return
						} // database/sql Tx statements close with their owner.
						e, finish = fixture.RoleTransactionBegin(t, p, b, c, mode)
						for i := range queries {
							execute(e, i)
						}
						fresh := queryOID(e)
						if fresh == first {
							t.Fatal("rolled-back principal identity reused", first)
						}
						finish(true)
						run(c, fixture.RoleTransactionExists("rq_new", true))
						run(c, fixture.RoleTransactionExists("rq_drop", false))
						// A genuinely held GRANT must recheck current execution authority.
						run(c, fixture.Exec("REVOKE rq_target FROM rq_member", 0))
						run(c, fixture.Exec("SET SESSION AUTHORIZATION rq_member", 0))
						want, detail := fixture.RoleMembershipDenied("rq_target", "grant")
						run(fixture.HeldStatement{Executor: c, Statement: stmts[3], SQL: queries[3]}, fixture.RoleReferenceError(queries[3], "42501", want, detail, false, "execute"))
						run(c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						execute(c, 3)
					})
				}
			})
		}
	}
}
