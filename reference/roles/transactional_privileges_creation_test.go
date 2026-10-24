package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Each control/handle combination owns a fresh fixture and a genuinely held
// symbolic utility prepared before its target exists.
func TestReferenceTransactionalPrivilegesCreation(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("goTx=%v/held=%v/commit=%v", goTx, held, commit), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tx_reader"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							run(c, fixture.Exec("CREATE ROLE tx_reader", 0))
							run(c, fixture.Exec("GRANT USAGE ON SCHEMA public TO tx_reader", 0))
							run(peer, fixture.Exec("SET ROLE tx_reader", 0))
							q := "GRANT SELECT(a) ON tx_created TO tx_reader"
							var utility *sql.Stmt
							if held {
								var err error
								utility, err = c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer utility.Close()
							}
							var e fixture.Executor = c
							finish := func() { run(c, fixture.Exec(map[bool]string{true: "COMMIT", false: "ROLLBACK"}[commit], 0)) }
							if goTx {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								}
							} else {
								run(c, fixture.Exec("BEGIN", 0))
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							run(e, fixture.Exec("CREATE TABLE tx_created(a INT,b INT)", 0))
							run(e, fixture.Exec("INSERT INTO tx_created VALUES(7,9)", 1))
							source := e
							if held {
								source = fixture.HeldStatement{Executor: e, Statement: utility, SQL: q}
							}
							run(source, fixture.Exec(q, 0))
							run(e, fixture.Exec("SET ROLE tx_reader", 0))
							run(e, fixture.Select("SELECT a FROM tx_created", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(e, fixture.Exec("SAVEPOINT column_probe", 0))
							denied := fixture.RoleGrantDenied("SELECT b FROM tx_created", "tx_created")
							denied.SelectRows = true
							run(e, denied)
							run(e, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
							run(e, fixture.Exec("ROLLBACK TO column_probe", 0))
							run(e, fixture.Exec("RESET ROLE", 0))
							run(peer, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_created'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							finish()
							if commit {
								run(peer, fixture.Select("SELECT a FROM tx_created", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
								run(peer, denied)
							} else {
								run(peer, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_created'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
								run(c, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tx_created','_tx_created')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							}
							run(peer, fixture.Exec("RESET ROLE", 0))
							run(c, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
						})
					}
				})
			}
		}
	}
}
