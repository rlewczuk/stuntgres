package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintFeatureSmoke(t *testing.T) {
	constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		constraintModeGraph(run)
		child := constraintModeOID(t, p, c, "child", "nn")
		detach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT")
		attach := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn INHERIT")
		validateConstraintHeldRun(t, p, b, detach, fixture.Exec(detach.SQL, 0))
		run(constraintModeNN("t", "nn", true, true, 0, true))
		run(constraintModeNN("child", "nn", false, true, 0, true))
		run(constraintModeNN("grandchild", "nn", false, false, 1, true))
		constraintModeSameOID(t, p, c, "child", "nn", child)
		validateConstraintHeldRun(t, p, b, attach, fixture.Exec(attach.SQL, 0))
		run(constraintModeNN("t", "nn", false, true, 0, true))
		run(constraintModeNN("child", "nn", false, true, 1, true))
		constraintModeSameOID(t, p, c, "child", "nn", child)
		insert := addConstraintHeld(t, p, b, c, "INSERT INTO child VALUES($1)")
		validateConstraintHeldRun(t, p, b, insert, fixture.ErrorDiagnostic(insert.SQL, fixture.PrimaryKeyReferenceNull("child", "a", "null"), nil))
		before := constraintModeSnapshot(t, p, b, c)
		late := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT, ALTER CONSTRAINT missing INHERIT")
		validateConstraintHeldRun(t, p, b, late, fixture.ErrorDiagnostic(late.SQL, constraintModeMissing("t", "missing")))
		constraintModeUnchanged(t, p, b, c, before)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE foreign_child(a INT CONSTRAINT fk REFERENCES p)", 0))
		timing := addConstraintHeld(t, p, b, c, "ALTER TABLE foreign_child ALTER CONSTRAINT fk DEFERRABLE INITIALLY DEFERRED")
		validateConstraintHeldRun(t, p, b, timing, fixture.Exec(timing.SQL, 0))
		fkInsert := addConstraintHeld(t, p, b, c, "INSERT INTO foreign_child VALUES($1)")
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		validateConstraintHeldRun(t, p, b, fkInsert, fixture.Exec(fkInsert.SQL, 1, 9))
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		validateConstraintHeldRun(t, p, b, fkInsert, fixture.Exec(fkInsert.SQL, 1, 10))
		run(fixture.ErrorDiagnostic("COMMIT", addConstraintFK("foreign_child", "fk", "a", "10", "p")))
		for _, q := range []string{"CREATE TABLE invalid_root(a INT)", "ALTER TABLE invalid_root ADD CONSTRAINT invalid_nn NOT NULL a NOT VALID"} {
			run(fixture.Exec(q, 0))
			if q == "CREATE TABLE invalid_root(a INT)" {
				run(fixture.Exec("INSERT INTO invalid_root VALUES(NULL)", 1))
			}
		}
		run(fixture.Exec("ALTER TABLE invalid_root ALTER CONSTRAINT invalid_nn NO INHERIT", 0))
		check := func() {
			run(constraintModeNN("t", "nn", false, true, 0, true))
			run(constraintModeNN("child", "nn", false, true, 1, true))
			run(constraintModeNN("grandchild", "nn", false, false, 1, true))
			run(constraintModeNN("invalid_root", "invalid_nn", true, true, 0, false))
			run(fixture.Select("SELECT conname,condeferrable,condeferred,conenforced,convalidated FROM pg_constraint WHERE conrelid='foreign_child'::regclass AND conname='fk'", []string{"conname", "condeferrable", "condeferred", "conenforced", "convalidated"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"fk", true, true, true, true}}))
			run(fixture.Select("SELECT a FROM foreign_child ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			run(fixture.Select("SELECT a FROM invalid_root", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		}
		check()
		if !b.PG {
			dump := addConstraintDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			enforcementSameDump(t, p, dump)
			check()
		}
	})
}
