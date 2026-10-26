package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintTransactions(t *testing.T) {
	for _, tc := range validateConstraintCases() {
		for _, repair := range []string{"replace", "delete"} {
			t.Run(tc.name+"/"+repair, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES($1,'bad')", 1, tc.bad))
					run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
					name := validateConstraintName(tc)
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT "+name)
					before := validateConstraintSnapshot(t, p, c)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT repair", 0))
					validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, tc.build))
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO SAVEPOINT repair", 0))
					run(validateConstraintFlag("t", name, false))
					if repair == "replace" {
						run(fixture.Exec("UPDATE t SET a=1", 1))
					} else {
						run(fixture.Exec("DELETE FROM t", 1))
						run(fixture.Exec("INSERT INTO t VALUES(1,'bad')", 1))
					}
					// Own replacements/deletes mask ended historical versions; own insert is scanned.
					run(fixture.Exec("INSERT INTO t VALUES(2,'own')", 1))
					run(fixture.Exec("SAVEPOINT promote", 0))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(validateConstraintFlag("t", name, true))
					run(fixture.Exec("ROLLBACK TO SAVEPOINT promote", 0))
					run(validateConstraintFlag("t", name, false))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(fixture.Exec("ROLLBACK", 0))
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
						t.Fatal("rollback retained promotion")
					}
					run(addConstraintRows([]any{tc.bad, "bad"}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("DELETE FROM t", 1))
					run(fixture.Exec("INSERT INTO t VALUES(1,'committed')", 1))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					run(fixture.Exec("COMMIT", 0))
					run(validateConstraintFlag("t", name, true))
					validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "committed"})
				})
			})
		}
		t.Run(tc.name+"/own_bad_before_add", func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES($1,'bad')", 1, tc.bad))
				run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t VALIDATE CONSTRAINT "+validateConstraintName(tc), tc.build)))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(validateConstraintFlag("t", validateConstraintName(tc), false))
				run(fixture.Exec("ROLLBACK", 0))
				run(addConstraintRows())
			})
		})
	}
	t.Run("late_failure_companion_atomicity", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID, ADD CONSTRAINT dirty CHECK(a>9) NOT VALID", 0))
			before := validateConstraintSnapshot(t, p, c)
			d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "dirty" of relation "t" is violated by some row`)
			d.Schema, d.Table, d.Constraint = "public", "t", "dirty"
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT ck, ADD COLUMN companion INT DEFAULT 7, VALIDATE CONSTRAINT dirty")
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, d))
			if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
				t.Fatal("late scan published earlier promotion")
			}
			run(fixture.Select("SELECT * FROM t", []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}}))
			run(fixture.Exec("UPDATE t SET a=10", 1))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(fixture.Select("SELECT * FROM t", []string{"a", "note", "companion"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(10), "old", int64(7)}}))
			run(validateConstraintFlag("t", "ck", true))
			run(validateConstraintFlag("t", "dirty", true))
		})
	})
	t.Run("rewrite_uses_private_source", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(0,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("UPDATE t SET a=1", 1))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER a TYPE BIGINT, VALIDATE CONSTRAINT ck", 0)))
			run(fixture.Select("SELECT a,note FROM t", []string{"a", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "old"}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(validateConstraintFlag("t", "ck", false))
			run(addConstraintRows([]any{int64(0), "old"}))
		})
	})
}
