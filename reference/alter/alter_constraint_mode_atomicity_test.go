package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintAtomicity(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				constraintModeGraph(run)
				before := constraintModeSnapshot(t, p, b, c)
				q := "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT, ALTER CONSTRAINT missing INHERIT"
				step := fixture.ErrorDiagnostic(q, constraintModeMissing("t", "missing"))
				if held {
					h := addConstraintHeld(t, p, b, c, q)
					validateConstraintHeldRun(t, p, b, h, step)
				} else {
					run(step)
				}
				constraintModeUnchanged(t, p, b, c, before)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT original", 0))
				run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT, ADD CONSTRAINT companion CHECK(a>0)", 0))
				run(constraintModeNN("t", "nn", true, true, 0, true))
				run(constraintModeNN("child", "nn", false, true, 0, true))
				run(fixture.Exec("ROLLBACK TO original", 0))
				run(fixture.Exec("COMMIT", 0))
				constraintModeUnchanged(t, p, b, c, before)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT, ADD CONSTRAINT companion CHECK(a>0)", 0))
				run(fixture.Exec("COMMIT", 0))
				run(constraintModeNN("t", "nn", true, true, 0, true))
				run(constraintModeNN("child", "nn", false, true, 0, true))
				run(constraintModeNN("grandchild", "nn", false, false, 1, true))
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"companion"}, {"nn"}}))
			})
		})
	}
	t.Run("later_child_null_restores_earlier_child_and_root", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", 0))
			run(fixture.Exec("CREATE TABLE first_child() INHERITS(t)", 0))
			run(fixture.Exec("CREATE TABLE last_child() INHERITS(t)", 0))
			run(fixture.Exec("INSERT INTO first_child VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO last_child VALUES(NULL)", 1))
			before := constraintModeSnapshot(t, p, b, c)
			q := "ALTER TABLE t ADD CONSTRAINT companion CHECK(a>0) NOT VALID, ALTER CONSTRAINT nn INHERIT"
			h := addConstraintHeld(t, p, b, c, q)
			validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, constraintModeScanNull("last_child")))
			constraintModeUnchanged(t, p, b, c, before)
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("SAVEPOINT s", 0))
			validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, constraintModeScanNull("last_child")))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("COMMIT", 0))
			constraintModeUnchanged(t, p, b, c, before)
			run(fixture.Exec("UPDATE last_child SET a=2", 1))
			validateConstraintHeldRun(t, p, b, h, fixture.Exec(q, 0))
			run(constraintModeNN("t", "nn", false, true, 0, true))
			run(constraintModeNN("first_child", "nn", false, false, 1, true))
			run(constraintModeNN("last_child", "nn", false, false, 1, true))
			run(fixture.ErrorDiagnostic("INSERT INTO last_child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("last_child", "a", "null")))
		})
	})
}
