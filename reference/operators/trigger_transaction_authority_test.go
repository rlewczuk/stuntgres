package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerAuthority(t *testing.T) {
	commands := []struct{ name, query, noun string }{
		{"create", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)", ""},
		{"replace", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)", ""},
		{"rename", "ALTER TRIGGER a ON tr_data RENAME TO renamed", "table"},
		{"drop", "DROP TRIGGER a ON tr_data", "relation"},
		{"depends", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", "relation"},
		{"no-depends", "ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", "relation"},
	}
	for _, api := range []string{"auto", "sql", "go"} {
		for _, command := range commands {
			privileges := []string{"owner"}
			if command.noun == "" {
				privileges = []string{"table", "function"}
			}
			for _, privilege := range privileges {
				t.Run(fmt.Sprintf("%s/%s/%s", api, command.name, privilege), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tr_tx_actor", "tr_tx_other"})
					triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						triggerSetup(t, p, b, c)
						userOperatorExec(t, p, b, c, "CREATE ROLE tr_tx_actor", "CREATE ROLE tr_tx_other", "GRANT USAGE ON SCHEMA public TO tr_tx_actor,tr_tx_other", "GRANT TRIGGER,INSERT,SELECT ON tr_data TO tr_tx_actor", "REVOKE EXECUTE ON FUNCTION tr_add() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION tr_add() TO tr_tx_actor", "CREATE EXTENSION unaccent")
						if command.name != "create" {
							userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent")
						}
						if command.noun != "" {
							userOperatorExec(t, p, b, c, "ALTER TABLE tr_data OWNER TO tr_tx_actor")
						}
						if command.noun != "" {
							userOperatorExec(t, p, b, c, "GRANT tr_tx_other TO tr_tx_actor")
						}
						admin, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer admin.Close()
						defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
						defer c.ExecContext(context.Background(), "ROLLBACK")
						userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION tr_tx_actor")
						stmt, err := c.PrepareContext(p.Ctx, command.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						// Preparation is not authorization: alter authority after retaining the recipe.
						deny, restore, message := "", "", ""
						switch privilege {
						case "table":
							deny = "REVOKE TRIGGER ON tr_data FROM tr_tx_actor"
							restore = "GRANT TRIGGER ON tr_data TO tr_tx_actor"
							message = "permission denied for table tr_data"
						case "function":
							deny = "REVOKE EXECUTE ON FUNCTION tr_add() FROM tr_tx_actor"
							restore = "GRANT EXECUTE ON FUNCTION tr_add() TO tr_tx_actor"
							message = "permission denied for function tr_add"
						case "owner":
							deny = "SET ROLE tr_tx_other"
							restore = "RESET ROLE"
							message = "must be owner of " + command.noun + " tr_data"
						}
						if privilege == "owner" {
							userOperatorExec(t, p, b, c, deny)
						} else {
							fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(deny, 0))
						}
						var e fixture.Executor = c
						var tx *sql.Tx
						if api == "go" {
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							stmt = tx.StmtContext(p.Ctx, stmt)
							defer stmt.Close()
						} else if api == "sql" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						if api != "auto" {
							run(fixture.Exec("SAVEPOINT authority", 0))
						}
						held := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: command.query}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, triggerDDLError(command.query, "42501", message, false))
						if api != "auto" {
							run(triggerDDLError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
							run(fixture.Exec("ROLLBACK TO authority", 0))
						}
						if privilege == "owner" {
							run(fixture.Exec(restore, 0))
						} else {
							fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(restore, 0))
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(command.query, 0))
						want := int64(10)
						if command.name == "create" || command.name == "replace" {
							want = 12
						}
						if command.name == "drop" {
							want = 5
						}
						run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), want}))
						if api != "auto" {
							if tx != nil {
								if err := tx.Commit(); err != nil {
									t.Fatal(err)
								}
							} else {
								run(fixture.Exec("COMMIT", 0))
							}
						}
					})
				})
			}
		}
	}
}
