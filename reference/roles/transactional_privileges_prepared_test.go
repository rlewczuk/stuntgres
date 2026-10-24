package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesPrepared(t *testing.T) {
	transactionalPrivilegesPreparedTransactions(t)
	transactionalPrivilegesReadOnlyGo(t)
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
		for _, inside := range []bool{false, true} {
			for _, q := range []string{"GRANT SELECT ON tx_absent TO tx_absent_role", "REVOKE SELECT ON tx_absent FROM tx_absent_role", "ALTER DEFAULT PRIVILEGES FOR ROLE tx_absent_role IN SCHEMA tx_absent GRANT SELECT ON TABLES TO tx_absent_role"} {
				if inside {
					run(fixture.Exec("BEGIN READ ONLY", 0))
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("syntax-only Prepare", q, err)
				}
				if !inside {
					run(fixture.Exec("BEGIN READ ONLY", 0))
				}
				command := strings.Fields(q)[0]
				if command == "ALTER" {
					command = "ALTER DEFAULT PRIVILEGES"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + command + " in a read-only transaction"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				stmt.Close()
				run(fixture.ImplicitError("GRANT SELECT ON tx_absent TO tx_absent_role", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				run(fixture.Exec("ROLLBACK", 0))
			}
		}
		run(fixture.Exec("CREATE TABLE t(a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		query := "SELECT a FROM t"
		reader, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		held := fixture.HeldStatement{Executor: c, Statement: reader, SQL: query}
		want := fixture.Select(query, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET ROLE", 0))
		revoke := "REVOKE SELECT ON t FROM rg_reader"
		utility, err := c.PrepareContext(p.Ctx, revoke)
		if err != nil {
			t.Fatal(err)
		}
		defer utility.Close()
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: utility, SQL: revoke}, b.Recorder, fixture.Exec(revoke, 0))
		run(fixture.Exec("SAVEPOINT read_probe", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		denied := fixture.RoleGrantDenied(query, "t")
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("ROLLBACK TO read_probe", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: utility, SQL: revoke}, b.Recorder, fixture.Exec(revoke, 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		run(fixture.Exec("SET ROLE rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
