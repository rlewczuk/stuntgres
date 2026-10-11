package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameAuthorization(t *testing.T) {
	for _, k := range constraintRenameKinds {
		t.Run(k.kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rename_owner", "rename_intruder"})
			fixture.UniqueReferenceSchemaCleanup(t, "rename_schema")
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t("+k.declaration+")", 0))
				run(fixture.Exec("CREATE ROLE rename_intruder", 0))
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rename_intruder", 0))
				}
				q := "ALTER TABLE t RENAME CONSTRAINT old TO new"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				run(fixture.Exec("SET SESSION AUTHORIZATION rename_intruder", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, constraintRenameError("42501", "must be owner of table t")))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(fixture.ErrorDiagnostic(q, constraintRenameError("25006", "cannot execute ALTER TABLE in a read-only transaction")))
				run(fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
				// Namespace ACL provisioning is PostgreSQL-only; genuine native authority
				// APIs exercise this case in the supplemental package units.
				if b.PG {
					run(fixture.Exec("CREATE ROLE rename_owner", 0))
					run(fixture.Exec("CREATE SCHEMA rename_schema AUTHORIZATION rename_owner", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION rename_owner", 0))
					run(fixture.Exec("CREATE TABLE rename_schema.t("+k.declaration+")", 0))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("REVOKE CREATE ON SCHEMA rename_schema FROM rename_owner", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION rename_owner", 0))
					run(fixture.Prepared(fixture.Exec("ALTER TABLE rename_schema.t RENAME CONSTRAINT old TO new", 0)))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
			})
		})
	}
}
