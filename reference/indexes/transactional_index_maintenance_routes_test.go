package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func indexTxMaintenanceSQL(action string) string {
	switch action {
	case "rename":
		return "ALTER INDEX ix RENAME TO fresh"
	case "set":
		return "ALTER INDEX ix SET(fillfactor=80,deduplicate_items=off)"
	case "reset":
		return "ALTER INDEX ix RESET(fillfactor,deduplicate_items)"
	case "drop":
		return "DROP INDEX ix"
	}
	panic(action)
}

func indexTxMaintenanceState(action string, changed bool) fixture.Step {
	name := "ix"
	var options any
	if action == "reset" {
		options = "{fillfactor=80,deduplicate_items=off}"
	}
	rows := [][]any{{name, options}}
	if changed {
		switch action {
		case "rename":
			rows = [][]any{{"fresh", nil}}
		case "set":
			rows = [][]any{{"ix", "{fillfactor=80,deduplicate_items=off}"}}
		case "reset":
			rows = [][]any{{"ix", nil}}
		case "drop":
			rows = nil
		}
	}
	return fixture.Select("SELECT relname,reloptions::text FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('ix','fresh') ORDER BY relname", []string{"relname", "reloptions"}, []string{"NAME", "TEXT"}, rows)
}

func indexTxMaintenanceSeed(run func(fixture.Step), action string) {
	run(fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,'a'),(2,'b'),(NULL,NULL)", 3))
	run(fixture.Exec("CREATE UNIQUE INDEX ix ON t(n)", 0))
	if action == "reset" {
		run(fixture.Exec("ALTER INDEX ix SET(fillfactor=80,deduplicate_items=off)", 0))
	}
}

func TestReferenceTransactionalIndexMaintenanceRoutes(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		for _, mode := range []string{"autocommit", "sql-begin", "begin-tx"} {
			for _, route := range []string{"direct", "held-before", "held-inside"} {
				for _, call := range []string{"exec-context", "query-context", "exec", "query"} {
					// sql.Conn deliberately exposes only Context methods.
					if mode == "sql-begin" && (call == "exec" || call == "query") {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/%s/%s", action, mode, route, call), func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name, func(t *testing.T) {
								setup := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
								indexTxMaintenanceSeed(setup, action)
								q := indexTxMaintenanceSQL(action)
								var executor fixture.Executor = b.DB
								var stmt *sql.Stmt
								var tx *sql.Tx
								var conn *sql.Conn
								var err error
								if mode == "sql-begin" {
									conn, err = b.DB.Conn(p.Ctx)
									if err != nil {
										t.Fatal(err)
									}
									defer conn.Close()
									defer conn.ExecContext(p.Ctx, "ROLLBACK")
									executor = conn
								}
								if route == "held-before" {
									if conn != nil {
										stmt, err = conn.PrepareContext(p.Ctx, q)
									} else {
										stmt, err = b.DB.PrepareContext(p.Ctx, q)
									}
									if err != nil {
										t.Fatal(err)
									}
									defer stmt.Close()
								}
								if mode == "begin-tx" {
									tx, err = b.DB.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									executor = tx
									if stmt != nil {
										stmt = tx.StmtContext(p.Ctx, stmt)
										defer stmt.Close()
									}
								} else if mode == "sql-begin" {
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec("BEGIN", 0))
								}
								if route == "held-inside" {
									switch {
									case tx != nil:
										stmt, err = tx.PrepareContext(p.Ctx, q)
									case conn != nil:
										stmt, err = conn.PrepareContext(p.Ctx, q)
									default:
										stmt, err = b.DB.PrepareContext(p.Ctx, q)
									}
									if err != nil {
										t.Fatal(err)
									}
									defer stmt.Close()
								}
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, indexTxMaintenanceState(action, false))
								if stmt != nil {
									executor = fixture.HeldStatement{Executor: executor, Statement: stmt, SQL: q}
								}
								s := fixture.Exec(q, 0)
								if call == "query" || call == "query-context" {
									s = fixture.Select(q, nil, nil, nil)
								}
								s.NonContext = call == "exec" || call == "query"
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, indexTxMaintenanceState(action, true))
								if stmt != nil {
									var restore string
									switch action {
									case "rename":
										restore = "ALTER INDEX fresh RENAME TO ix"
									case "set":
										restore = "ALTER INDEX ix RESET(fillfactor,deduplicate_items)"
									case "reset":
										restore = "ALTER INDEX ix SET(fillfactor=80,deduplicate_items=off)"
									case "drop":
										restore = "CREATE UNIQUE INDEX ix ON t(n)"
									}
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(restore, 0))
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, indexTxMaintenanceState(action, false))
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, indexTxMaintenanceState(action, true))
								}
								if mode != "autocommit" {
									setup(indexTxMaintenanceState(action, false))
								}
								if tx != nil {
									if err := tx.Commit(); err != nil {
										t.Fatal(err)
									}
								} else if conn != nil {
									fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("COMMIT", 0))
								}
								setup(indexTxMaintenanceState(action, true))
								setup(fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {nil, nil}}))
							})
						}
					})
				}
			}
		}
	}
}
