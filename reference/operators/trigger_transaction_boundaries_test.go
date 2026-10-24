package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerBoundaries(t *testing.T) {
	commands := []struct{ q, tag string }{
		{"CREATE TRIGGER absent BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION tr_missing()", "CREATE TRIGGER"},
		{"CREATE OR REPLACE TRIGGER absent BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION tr_missing()", "CREATE TRIGGER"},
		{"ALTER TRIGGER absent ON %s RENAME TO renamed", "ALTER TRIGGER"},
		{"ALTER TRIGGER absent ON %s DEPENDS ON EXTENSION no_extension", "ALTER TRIGGER"},
		{"ALTER TRIGGER absent ON %s NO DEPENDS ON EXTENSION no_extension", "ALTER TRIGGER"},
		{"DROP TRIGGER absent ON %s", "DROP TRIGGER"},
	}
	for i, command := range commands {
		for _, api := range []string{"sql", "go"} {
			for _, held := range []bool{false, true} {
				for _, target := range []string{"no_table", "tr_temp"} {
					t.Run(fmt.Sprintf("%d/%s/held=%v/%s", i, api, held, target), func(t *testing.T) {
						triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							triggerDDLChain(t, p, b, c)
							userOperatorExec(t, p, b, c, "CREATE TEMP TABLE tr_temp(id integer,v integer)")
							defer c.ExecContext(context.Background(), "ROLLBACK")
							q := fmt.Sprintf(command.q, target)
							var e fixture.Executor = c
							var tx *sql.Tx
							var err error
							if api == "go" {
								tx, err = c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
							} else {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
							}
							var stmt *sql.Stmt
							if held {
								stmt, err = e.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal("quiet prepare", err)
								}
								defer stmt.Close()
							}
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							run(fixture.Exec("SAVEPOINT boundary", 0))
							var execute fixture.Executor = e
							if held {
								execute = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
							}
							fixture.RunStep(t, p.Ctx, execute, b.Recorder, triggerDDLError(q, "25006", "cannot execute "+command.tag+" in a read-only transaction", false))
							fixture.RunStep(t, p.Ctx, execute, b.Recorder, triggerDDLError(q, "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
							run(fixture.Exec("ROLLBACK TO boundary", 0))
							run(triggerRows("SELECT id,v FROM tr_data"))
							if tx != nil {
								if err := tx.Rollback(); err != nil {
									t.Fatal(err)
								}
							} else {
								run(fixture.Exec("ROLLBACK", 0))
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(16)}))
							if !b.PG {
								ctx, cancel := context.WithCancel(p.Ctx)
								cancel()
								_, err := c.ExecContext(ctx, q)
								if err != context.Canceled {
									t.Fatalf("pre-cancel: %v", err)
								}
							}
						})
					})
				}
			}
		}
	}
}
