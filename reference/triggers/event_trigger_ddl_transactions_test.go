package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLTransactions(t *testing.T) {
	for _, action := range []struct{ name, sql string }{
		{"state", "ALTER EVENT TRIGGER et_ddl_tx ENABLE REPLICA"},
		{"rename", "ALTER EVENT TRIGGER et_ddl_tx RENAME TO et_ddl_tx_new"},
		{"owner", "ALTER EVENT TRIGGER et_ddl_tx OWNER TO et_ddl_tx_owner"},
		{"drop", "DROP EVENT TRIGGER et_ddl_tx"},
	} {
		for _, ending := range []string{"rollback", "savepoint", "commit"} {
			t.Run(action.name+"/"+ending, func(t *testing.T) {
				eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
					fixture.RoleReferenceCleanup(t, []string{"et_ddl_tx_owner"})
					eventTriggerScenario(t, []string{"et_ddl_tx", "et_ddl_tx_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						d := eventDDLCase{t, p, b, c, route}
						d.exec("CREATE ROLE et_ddl_tx_owner SUPERUSER")
						d.seed("et_ddl_tx", "ddl_command_end", "'ALTER TABLE'")
						old := d.identity("et_ddl_tx", "ddl_command_end", "O", `{"ALTER TABLE"}`)
						next := old
						switch action.name {
						case "state":
							next.mode = "R"
						case "rename":
							next.name = "et_ddl_tx_new"
						case "owner":
							next.owner = d.role("et_ddl_tx_owner")
						}
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						h := d.held("SELECT oid,evtname,evtevent,evtowner,evtfoid,evtenabled,evttags FROM pg_event_trigger ORDER BY evtname")
						defer h.Statement.Close()
						eligible := d.held("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
						defer eligible.Statement.Close()
						d.exec("BEGIN", "SAVEPOINT et_ddl_tx_save")
						d.utility(action.sql)
						if action.name == "drop" {
							d.catalog(h)
						} else {
							d.catalog(h, next)
						}
						d.catalog(peer, old)
						d.dependencies(old, action.name != "drop")
						d.audit()
						switch ending {
						case "rollback":
							d.exec("ROLLBACK")
						case "savepoint":
							d.exec("ROLLBACK TO et_ddl_tx_save", "COMMIT")
						case "commit":
							d.exec("COMMIT")
						}
						want := old
						if ending == "commit" {
							want = next
						}
						if ending == "commit" && action.name == "drop" {
							d.catalog(h)
							d.catalog(peer)
						} else {
							d.catalog(h, want)
							d.catalog(peer, want)
						}
						d.dependencies(old, ending != "commit" || action.name != "drop")
						d.heldExec(eligible)
						fires := ending != "commit" || action.name != "drop" && action.name != "state"
						if fires {
							d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
						} else {
							d.audit()
						}
						if ending != "commit" || action.name == "drop" {
							d.utility("DROP ROLE et_ddl_tx_owner")
						} else if action.name == "owner" {
							d.failure("DROP ROLE et_ddl_tx_owner", eventDDLRoleDependency("et_ddl_tx_owner", "et_ddl_tx"), false)
						}
					})
				})
			})
		}
		t.Run(action.name+"/readonly_and_cancel", func(t *testing.T) {
			eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
				fixture.RoleReferenceCleanup(t, []string{"et_ddl_tx_owner"})
				eventTriggerScenario(t, []string{"et_ddl_tx", "et_ddl_tx_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					d := eventDDLCase{t, p, b, c, route}
					d.exec("CREATE ROLE et_ddl_tx_owner SUPERUSER")
					d.seed("et_ddl_tx", "ddl_command_end", "'ALTER TABLE'")
					i := d.identity("et_ddl_tx", "ddl_command_end", "O", `{"ALTER TABLE"}`)
					heldMutation := d.held(action.sql)
					defer heldMutation.Statement.Close()
					d.audit()
					d.exec("BEGIN READ ONLY")
					command := "ALTER EVENT TRIGGER"
					if action.name == "drop" {
						command = "DROP EVENT TRIGGER"
					}
					d.failure(action.sql, eventTriggerDiagnostic("25006", "cannot execute "+command+" in a read-only transaction"), false)
					d.failure("SELECT 1", eventTriggerDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), true)
					d.exec("ROLLBACK", "BEGIN")
					d.failure("ALTER EVENT TRIGGER et_absent DISABLE", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`), false)
					aborted := fixture.ErrorDiagnostic(action.sql, eventTriggerDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					aborted.ErrorStage, aborted.SelectRows = "execute", route.query
					fixture.RunStep(t, p.Ctx, heldMutation, b.Recorder, aborted)
					d.failure(action.sql, eventTriggerDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), true)
					d.exec("ROLLBACK", "BEGIN")
					d.utility("DROP EVENT TRIGGER IF EXISTS et_absent")
					d.run(fixture.Select("SELECT 1 AS ok", []string{"ok"}, []string{"INT4"}, [][]any{{int64(1)}}))
					d.exec("COMMIT")
					d.catalog(c, i)
					d.dependencies(i, true)
					d.audit()
					if b.PG {
						return
					}
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := error(context.Canceled)
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						var executor fixture.Executor = c
						var stmt *sql.Stmt
						if route.prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, action.sql)
							if err != nil {
								cancel()
								t.Fatal(err)
							}
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: action.sql}
						}
						var err error
						if route.query {
							rows, e := executor.QueryContext(ctx, action.sql)
							err = e
							if rows != nil {
								rows.Close()
								t.Error("canceled query returned rows")
							}
						} else {
							result, e := executor.ExecContext(ctx, action.sql)
							err = e
							if result != nil {
								t.Error("canceled exec returned result")
							}
						}
						if stmt != nil {
							stmt.Close()
						}
						<-ctx.Done()
						if !errors.Is(err, want) || !errors.Is(ctx.Err(), want) {
							t.Fatalf("cancellation: %v / %v, want %v", err, ctx.Err(), want)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("context error fabricated SQLSTATE", state.SQLState())
						}
						cancel()
						d.catalog(c, i)
						d.dependencies(i, true)
						d.audit()
					}
					d.utility("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
					d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
				})
			})
		})
	}
}
