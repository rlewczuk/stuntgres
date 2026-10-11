package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateAuthorizationSchema(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateExec(t, f, f.A, "CREATE ROLE truncate_schema_reader", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP SCHEMA hidden CASCADE")
			f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_schema_reader")
			f.A.ExecContext(p.Ctx, "DROP ROLE truncate_schema_reader")
		}()
		truncateExec(t, f, f.A, "CREATE SCHEMA hidden", 0)
		truncateExec(t, f, f.A, `CREATE TABLE hidden."Mixed Table"(id INT)`, 0)
		truncateExec(t, f, f.A, `INSERT INTO hidden."Mixed Table" VALUES(9)`, 1)
		truncateExec(t, f, f.A, `GRANT TRUNCATE ON hidden."Mixed Table" TO truncate_schema_reader`, 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_schema_reader", 0)
		for _, q := range []string{`TRUNCATE hidden."Mixed Table"`, `TRUNCATE hidden.missing`} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, truncateWant("42501", "permission denied for schema hidden"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				truncateRun(t, f, f.A, s)
			}
		}
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, `hidden."Mixed Table"`, 9)
		// Native schema GRANT syntax is absent. Existing public ownership transfer
		// supplies genuine native visibility without transferring table ownership.
		if b.PG {
			truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA hidden TO truncate_schema_reader", 0)
		} else {
			truncateExec(t, f, f.A, "ALTER SCHEMA hidden OWNER TO truncate_schema_reader", 0)
		}
		truncateExec(t, f, f.A, "SET ROLE truncate_schema_reader", 0)
		truncateRun(t, f, f.A, fixture.Prepared(fixture.Exec(`TRUNCATE TABLE hidden."Mixed Table" CONTINUE IDENTITY`, 0)))
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, `hidden."Mixed Table"`)
	})
}
