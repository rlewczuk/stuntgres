package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaDiagnostics(t *testing.T) {
	transactionalSchemaReadOnlyAnalysis(t)
	transactionalSchemaAuthority(t)
	transactionalSchemaNotices(t)
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { transactionalSchemaRun(t, p, b, c, held, s) }
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("CREATE TABLE tx_diag(v INT)", 0))
					for _, tc := range []struct{ q, code, message string }{
						{"CREATE TABLE tx_diag(v INT)", "42P07", `relation "tx_diag" already exists`},
						{"ALTER TABLE tx_diag ADD COLUMN v INT", "42701", `column "v" of relation "tx_diag" already exists`},
						{"DROP TABLE tx_diag_absent", "42P01", `table "tx_diag_absent" does not exist`},
						{"CREATE TABLE tx_diag_absent.t(v INT)", "3F000", `schema "tx_diag_absent" does not exist`},
					} {
						run(fixture.Exec("SAVEPOINT recover", 0))
						d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						if tc.code == "3F000" {
							d.Position = "14"
						}
						s := fixture.ErrorDiagnostic(tc.q, d)
						s.ErrorStage = "execute"
						run(s)
						run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						run(fixture.Exec("ROLLBACK TO recover", 0))
					}
					run(fixture.Exec("ROLLBACK", 0))
					for _, tc := range []struct{ q, tag string }{{"CREATE TABLE tx_ro(v INT)", "CREATE TABLE"}, {"CREATE SCHEMA tx_ro", "CREATE SCHEMA"}, {"DROP TABLE tx_ro", "DROP TABLE"}, {"ALTER TABLE tx_ro RENAME TO tx_ro2", "ALTER TABLE"}} {
						run(fixture.Exec("BEGIN READ ONLY", 0))
						// Ordinary utility preparation is quiet, even when its target is absent.
						stmt, err := c.PrepareContext(p.Ctx, tc.q)
						if err != nil {
							t.Fatal(tc.q, err)
						}
						stmt.Close()
						s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tc.tag+" in a read-only transaction"))
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Exec("ROLLBACK", 0))
					}
				})
			}
		})
	}
}
