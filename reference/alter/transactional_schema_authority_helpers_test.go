package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func transactionalSchemaAuthority(t *testing.T) {
	t.Run("authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tx_schema_actor"})
		fixture.SchemaReferenceCleanup(t, "tx_acl_schema")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE tx_schema_actor", "CREATE SCHEMA tx_acl_schema", "GRANT USAGE ON SCHEMA tx_acl_schema TO tx_schema_actor", "CREATE TABLE tx_acl_schema.owned(v INT)", "CREATE TABLE tx_diag_parent(v INT)", "CREATE VIEW tx_diag_view AS SELECT v FROM tx_diag_parent"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT dependency", 0))
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table tx_diag_parent because other objects depend on it")
				d.Detail = "view tx_diag_view depends on table tx_diag_parent"
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				s := fixture.ErrorDiagnostic("DROP TABLE tx_diag_parent", d)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("ROLLBACK TO dependency", 0))
				run(fixture.Exec("SET ROLE tx_schema_actor", 0))
				for _, tc := range []struct{ q, message, position string }{{"CREATE TABLE tx_acl_schema.denied(v INT)", "permission denied for schema tx_acl_schema", "14"}, {"DROP TABLE tx_acl_schema.owned", "must be owner of table owned", ""}} {
					run(fixture.Exec("SAVEPOINT tx_authority", 0))
					stmt, err := c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal("quiet unauthorized utility prepare", err)
					}
					stmt.Close()
					run(fixture.Exec("RESET ROLE", 0))
					stmt, err = c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal("authorized preparation", err)
					}
					run(fixture.Exec("SET ROLE tx_schema_actor", 0))
					d := fixture.ControlsReferenceDiagnostic("42501", tc.message)
					d.Position = tc.position
					s := fixture.ErrorDiagnostic(tc.q, d)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, b.Recorder, s)
					stmt.Close()
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO tx_authority", 0))
				}
				fixture.LockExec(t, p.Ctx, b.DB, "GRANT CREATE ON SCHEMA tx_acl_schema TO tx_schema_actor", 0)
				for i, q := range []string{
					"CREATE TABLE tx_acl_schema.no_data AS SELECT v FROM tx_acl_schema.owned WITH NO DATA",
					"CREATE TABLE tx_acl_schema.with_data AS SELECT v FROM tx_acl_schema.owned WITH DATA",
					"SELECT v INTO tx_acl_schema.into_data FROM tx_acl_schema.owned",
				} {
					for _, prepare := range []bool{false, true} {
						run(fixture.Exec("SAVEPOINT source_authority", 0))
						if i == 0 {
							s := fixture.Exec(q, 0)
							s.Prepared = prepare
							run(s)
						} else {
							d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table owned")
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared = prepare
							s.ErrorStage = "execute"
							run(s)
						}
						run(fixture.Exec("ROLLBACK TO source_authority", 0))
					}
				}
				run(fixture.Exec("ROLLBACK", 0))
			})
		}
	})
}

func transactionalSchemaNotices(t *testing.T) {
	t.Run("conditional_notices", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "tx_notice")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("CREATE SCHEMA tx_notice", 0))
				run(fixture.Exec("CREATE TABLE tx_notice.present(v INT)", 0))
				for _, tc := range []struct{ q, code, message string }{
					{"CREATE SCHEMA IF NOT EXISTS tx_notice", "42P06", `schema "tx_notice" already exists, skipping`},
					{"CREATE TABLE IF NOT EXISTS tx_notice.present(v INT)", "42P07", `relation "present" already exists, skipping`},
					{"DROP TABLE IF EXISTS tx_notice.absent", "00000", `table "absent" does not exist, skipping`},
				} {
					notices = nil
					stmt, err := c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					if len(notices) != 0 {
						t.Fatal("Prepare emitted notice", notices)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, recorder, fixture.Exec(tc.q, 0))
					stmt.Close()
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.message}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("notices=%+v want=%+v", notices, want)
					}
				}
				run(fixture.Exec("ROLLBACK", 0))
			})
		}
	})
}
