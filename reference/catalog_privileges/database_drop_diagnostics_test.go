package reference_test

import (
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropDiagnostics(t *testing.T) {
	// Independent REL_18_6 gram.y/DropdbStmt and dbcommands.c/dropdb
	// expectations: missing is execution-time, IF EXISTS only suppresses missing,
	// owner precedes template/current, and read-only precedes transaction refusal.
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const name, actor = "dd_diagnostic_target", "dd_diagnostic_actor"
			fixture.RoleReferenceCleanup(t, []string{actor})
			names := []string{name}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, conn, &names}
					healthSQL := "SELECT 42 AS healthy"
					healthStmt, err := conn.PrepareContext(p.Ctx, healthSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer healthStmt.Close()
					health := fixture.HeldStatement{Executor: conn, Statement: healthStmt, SQL: healthSQL}
					checkHealth := func() {
						t.Helper()
						fixture.RunStep(t, p.Ctx, health, record, fixture.Select(healthSQL, []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
					}
					missingSQL := "DROP DATABASE " + name
					missingRoute := databaseDropPrepare(c, mode, missingSQL)
					databaseDropNotice(t, &notices, name, 0)
					for range 2 {
						missingRoute.run(c, fixture.ErrorDiagnostic(missingSQL, databaseCreateDiagnostic("3D000", "database \""+name+"\" does not exist")))
						c.absent(name)
						checkHealth()
					}
					query := "DROP DATABASE IF EXISTS " + name
					route := databaseDropPrepare(c, mode, query)
					databaseDropNotice(t, &notices, name, 0)
					route.run(c, fixture.Exec(query, 0))
					databaseDropNotice(t, &notices, name, 1)
					c.create(name, "", false)
					originalOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", name)
					c.run(fixture.Exec("CREATE ROLE "+actor+" CREATEDB", 0))
					c.run(fixture.Exec("GRANT CONNECT, CREATE ON DATABASE "+name+" TO "+actor, 0))
					c.run(fixture.Exec("ALTER DATABASE "+name+" IS_TEMPLATE true", 0))
					c.run(fixture.Exec("SET ROLE "+actor, 0))
					route.run(c, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42501", "must be owner of database "+name)))
					checkHealth()
					c.run(fixture.Exec("RESET ROLE", 0))
					route.run(c, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42809", "cannot drop a template database")))
					checkHealth()
					c.run(fixture.Exec("ALTER DATABASE "+name+" IS_TEMPLATE false", 0))
					victim, victimRaw, victimRecord := c.pool(name, "")
					victim.SetMaxIdleConns(0)
					owner, err := victim.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { owner.Close() })
					fixture.DatabaseReferenceOwners(t, p, conn, record, name, 1)
					busy := databaseCreateDiagnostic("55006", "database \""+name+"\" is being accessed by other users")
					busy.Detail = "There is 1 other session using the database."
					route.run(c, fixture.ErrorDiagnostic(query, busy))
					fixture.RunStep(t, p.Ctx, owner, victimRecord, fixture.Select("SELECT 7 AS alive", []string{"alive"}, []string{"INT4"}, [][]any{{int64(7)}}))
					if c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", name) != originalOID {
						t.Fatal("failed DROP replaced target")
					}
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
					databaseDropClose(t, victim, victimRaw)
					fixture.DatabaseReferenceOwners(t, p, conn, record, name, 0)
					for _, tx := range []struct{ begin, code, message string }{
						{"BEGIN", "25001", "DROP DATABASE cannot run inside a transaction block"},
						{"BEGIN READ ONLY", "25006", "cannot execute DROP DATABASE in a read-only transaction"},
					} {
						c.run(fixture.Exec(tx.begin, 0))
						route.run(c, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic(tx.code, tx.message)))
						c.run(fixture.Exec("ROLLBACK", 0))
						checkHealth()
					}
					c.run(fixture.Exec("SET default_transaction_read_only=on", 0))
					route.run(c, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("25006", "cannot execute DROP DATABASE in a read-only transaction")))
					c.run(fixture.Exec("SET default_transaction_read_only=off", 0))
					route.run(c, fixture.Exec(query, 0))
					c.absent(name)
					databaseDropNotice(t, &notices, name, 0)
					// SAME held SQL resolves the recreated owner, not the old target.
					c.create(name, "OWNER "+actor, false)
					c.run(fixture.Exec("SET ROLE "+actor, 0))
					route.run(c, fixture.Exec(query, 0))
					c.run(fixture.Exec("RESET ROLE", 0))
					route.run(c, fixture.Exec(query, 0))
					databaseDropNotice(t, &notices, name, 1)

					current := c.scalar("SELECT current_database()")
					for _, suffix := range []string{"", " WITH (FORCE, FORCE)"} {
						q := "DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(current) + suffix
						r := databaseDropPrepare(c, mode, q)
						for range 2 {
							r.run(c, fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("55006", "cannot drop the currently open database")))
							checkHealth()
						}
					}
					for _, suffix := range []string{"", " (FORCE)", " WITH (FORCE)", " (FORCE, FORCE)", " WITH (FORCE, FORCE)"} {
						q := "DROP DATABASE " + name + suffix
						r := databaseDropPrepare(c, mode, q)
						for range 2 {
							c.create(name, "", false)
							r.run(c, fixture.Exec(q, 0))
							c.absent(name)
							checkHealth()
						}
					}
					c.create(name, "", false)
					for _, malformed := range []struct{ suffix, token string }{
						{" ()", ")"}, {" WITH ()", ")"}, {" WITH FORCE", "FORCE"},
						{" (FORCE true)", "true"}, {" (FORCE=true)", "="},
						{" CASCADE", "CASCADE"}, {" RESTRICT", "RESTRICT"},
						{", dd_other", ","}, {" WITH (FORCE) trailing", "trailing"},
						{"; SELECT )", ")"},
					} {
						q := "DROP DATABASE " + name + malformed.suffix
						want := databaseCreateDiagnostic("42601", "syntax error at or near \""+malformed.token+"\"")
						want.Position = databaseCreatePosition(q, malformed.token, true)
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, want)
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							}
							c.run(s)
							c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{name}}, name))
							checkHealth()
						}
					}
					// A valid multi-statement message is an implicit transaction;
					// the earlier SET must not escape its failed DROP.
					before := c.scalar("SHOW application_name")
					c.run(fixture.ErrorDiagnostic("SET application_name='dd_prefix'; DROP DATABASE "+name, databaseCreateDiagnostic("25001", "DROP DATABASE cannot run inside a transaction block")))
					if c.scalar("SHOW application_name") != before {
						t.Fatal("failed DROP committed a prefix effect")
					}
					c.run(fixture.Exec("DROP DATABASE "+name, 0))
					c.absent(name)
					databaseDropNotice(t, &notices, name, 0)
					for _, decoded := range []string{"dépôt", "資料庫", `dd_"quoted`} {
						q := "DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(decoded)
						r := databaseDropPrepare(c, mode, q)
						databaseDropNotice(t, &notices, decoded, 0)
						r.run(c, fixture.Exec(q, 0))
						databaseDropNotice(t, &notices, decoded, 1)
						c.create(decoded, "", false)
						r.run(c, fixture.Exec(q, 0))
						c.absent(decoded)
					}
					// database/sql transaction admission is independent of textual
					// BEGIN. Preparation stays effect-free even inside this block.
					c.create(name, "", false)
					tx, err := conn.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					blockSQL := "DROP DATABASE " + name
					step := fixture.ErrorDiagnostic(blockSQL, databaseCreateDiagnostic("25001", "DROP DATABASE cannot run inside a transaction block"))
					step.ErrorStage = "execute"
					var executor fixture.Executor = tx
					if mode == "fresh" {
						step = fixture.Prepared(step)
					}
					if mode == "held" {
						stmt, err := tx.PrepareContext(p.Ctx, blockSQL)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
						executor = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: blockSQL}
					}
					fixture.RunStep(t, p.Ctx, executor, record, step)
					fixture.RunStep(t, p.Ctx, tx, record, fixture.ErrorDiagnostic("SELECT 1", databaseCreateDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					checkHealth()
					c.run(fixture.Exec("DROP DATABASE "+name, 0))
				})
			}
		})
	}
}
