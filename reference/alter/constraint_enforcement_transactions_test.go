package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementTransactions(t *testing.T) {
	for _, repair := range []string{"parent_insert", "child_update", "child_delete"} {
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(repair+"/"+finish, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
					before := enforcementSnapshot(t, p, b, c)
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9,'own')", 1))
					run(fixture.Exec("SAVEPOINT attempt", 0))
					validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, addConstraintFK("t", "fk", "a", "9", "p")))
					run(fixture.ErrorDiagnostic("SELECT a FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO attempt", 0))
					run(enforcementFlags("t", "fk", false, false))
					run(addConstraintRows([]any{int64(9), "own"}))
					q := "INSERT INTO p VALUES(9)"
					if repair == "child_update" {
						q = "UPDATE t SET a=1"
					}
					if repair == "child_delete" {
						q = "DELETE FROM t"
					}
					run(fixture.Exec(q, 1))
					run(fixture.Exec("SAVEPOINT repaired", 0))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(enforcementFlags("t", "fk", true, true))
					run(fixture.Exec("ROLLBACK TO repaired", 0))
					run(enforcementFlags("t", "fk", false, false))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(fixture.Exec(finish, 0))
					if finish == "ROLLBACK" {
						enforcementUnchanged(t, p, b, c, before)
					} else {
						run(enforcementFlags("t", "fk", true, true))
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(10,'bad')", addConstraintFK("t", "fk", "a", "10", "p")))
						if repair == "child_delete" {
							run(addConstraintRows())
						} else if repair == "child_update" {
							run(addConstraintRows([]any{int64(1), "own"}))
						} else {
							run(addConstraintRows([]any{int64(9), "own"}))
						}
					}
				})
			})
		}
	}
	for _, kind := range []string{"check", "fk"} {
		t.Run("savepoint_disable/"+kind, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				clause, name, disable := "CHECK(a>0)", "ck", "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED"
				if kind == "fk" {
					clause, name, disable = "FOREIGN KEY(a) REFERENCES p", "fk", "ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED"
				}
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+name+" "+clause, 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT original", 0))
				run(fixture.Prepared(fixture.Exec(disable, 0)))
				run(fixture.Exec("INSERT INTO t VALUES(0,'temporary')", 1))
				run(enforcementFlags("t", name, false, false))
				run(fixture.Exec("ROLLBACK TO original", 0))
				run(enforcementFlags("t", name, true, true))
				run(fixture.Exec("COMMIT", 0))
				enforcementUnchanged(t, p, b, c, before)
			})
		})
	}
	for _, companion := range []string{"ADD COLUMN added INT", "ADD CONSTRAINT companion CHECK(a>0) NOT ENFORCED", "ALTER COLUMN a TYPE BIGINT"} {
		t.Run(fmt.Sprintf("late_failure/%s", companion), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED, ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				before := enforcementSnapshot(t, p, b, c)
				run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t "+companion+", ALTER CONSTRAINT fk ENFORCED, VALIDATE CONSTRAINT ck", fixture.ControlsReferenceDiagnostic("55000", "cannot validate NOT ENFORCED constraint"))))
				enforcementUnchanged(t, p, b, c, before)
				run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
				run(enforcementFlags("t", "fk", true, true))
			})
		})
	}
}
