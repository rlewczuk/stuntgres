package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintModes(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, tc := range []struct {
			clause               string
			deferrable, deferred bool
		}{
			{"DEFERRABLE", true, false}, {"INITIALLY DEFERRED", true, true}, {"INITIALLY IMMEDIATE", false, false}, {"NOT DEFERRABLE", false, false}, {"DEFERRABLE INITIALLY DEFERRED", true, true},
		} {
			t.Run(tc.clause+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					oid := constraintModeOID(t, p, c, "t", "fk")
					triggers := enforcementTriggers(t, p, b, c, oid, true)
					q := "ALTER TABLE t ALTER CONSTRAINT fk " + tc.clause
					if held {
						h := addConstraintHeld(t, p, b, c, q)
						validateConstraintHeldRun(t, p, b, h, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Select("SELECT condeferrable,condeferred,conenforced,convalidated FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk'", []string{"condeferrable", "condeferred", "conenforced", "convalidated"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.deferrable, tc.deferred, true, true}}))
					constraintModeSameOID(t, p, c, "t", "fk", oid)
					if got := enforcementTriggers(t, p, b, c, oid, true); !reflect.DeepEqual(got, triggers) {
						t.Fatal("timing replaced trigger identity", got, triggers)
					}
					for _, id := range triggers {
						run(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE oid=$1::oid", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, [][]any{{tc.deferrable, tc.deferred}}, id))
					}
					insert := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1)")
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					bad := fixture.ErrorDiagnostic(insert.SQL, addConstraintFK("t", "fk", "a", "9", "p"), 9)
					if tc.deferred {
						validateConstraintHeldRun(t, p, b, insert, fixture.Exec(insert.SQL, 1, 9))
						run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
						run(fixture.Exec("COMMIT", 0))
						run(fixture.Exec("BEGIN", 0))
						validateConstraintHeldRun(t, p, b, insert, fixture.Exec(insert.SQL, 1, 10))
						run(fixture.ErrorDiagnostic("COMMIT", addConstraintFK("t", "fk", "a", "10", "p")))
						run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
					} else {
						validateConstraintHeldRun(t, p, b, insert, bad)
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, nil))
					}
				})
			})
		}
	}
	t.Run("enforcement_mixed_atomic", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT fk REFERENCES p DEFERRABLE)", 0))
			oid := constraintModeOID(t, p, c, "t", "fk")
			disable := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk INITIALLY DEFERRED NOT ENFORCED")
			validateConstraintHeldRun(t, p, b, disable, fixture.Exec(disable.SQL, 0))
			enforcementTriggers(t, p, b, c, oid, false)
			run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
			before := enforcementSnapshot(t, p, b, c)
			enable := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED INITIALLY IMMEDIATE")
			validateConstraintHeldRun(t, p, b, enable, fixture.ErrorDiagnostic(enable.SQL, addConstraintFK("t", "fk", "a", "9", "p")))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
			validateConstraintHeldRun(t, p, b, enable, fixture.Exec(enable.SQL, 0))
			constraintModeSameOID(t, p, c, "t", "fk", oid)
			enforcementTriggers(t, p, b, c, oid, true)
			run(fixture.Select("SELECT condeferrable,condeferred,conenforced,convalidated FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk'", []string{"condeferrable", "condeferred", "conenforced", "convalidated"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true, true}}))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(10)", addConstraintFK("t", "fk", "a", "10", "p")))
		})
	})
}
