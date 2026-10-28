package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementControls(t *testing.T) {
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := addConstraintSetup(t, p, b, c)
		run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT ENFORCED, ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
		run(fixture.Exec("INSERT INTO t VALUES(0,'bad check'),(9,'orphan')", 2))
		run(fixture.Exec("CREATE TABLE pending(a INT)", 0))
		run(fixture.Exec("INSERT INTO pending VALUES(0)", 1))
		run(fixture.Exec("ALTER TABLE pending ADD CONSTRAINT pending_ck CHECK(a>0) ENFORCED NOT VALID", 0))
		run(fixture.Exec("CREATE TABLE valid(a INT,CONSTRAINT valid_ck CHECK(a>0) ENFORCED)", 0))
		run(fixture.Exec("INSERT INTO valid VALUES(1)", 1))
		consumer := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a,note")
		check := func() {
			enforcementIdentity(t, p, b, c, "t", "ck", "CHECK ((a > 0)) NOT ENFORCED", false, false)
			oid := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
			enforcementTriggers(t, p, b, c, oid, false)
			run(enforcementFlags("pending", "pending_ck", true, false))
			run(enforcementFlags("valid", "valid_ck", true, true))
			run(addConstraintRows([]any{int64(0), "bad check"}, []any{int64(9), "orphan"}))
			validateConstraintHeldRun(t, p, b, consumer, addConstraintRows([]any{int64(0), "bad check"}, []any{int64(9), "orphan"}))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, consumer.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
			run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_constraint c ON d.classid='pg_constraint'::regclass AND d.objid=c.oid WHERE c.conrelid='t'::regclass AND c.conname='fk' AND d.refclassid='pg_class'::regclass AND d.refobjid=c.conindid AND d.deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		check()
		if !b.PG {
			dump := addConstraintDump(t, p)
			if strings.Count(string(dump), "NOT ENFORCED") != 2 || strings.Contains(string(dump), "NOT ENFORCED NOT VALID") || strings.Count(string(dump), "NOT VALID") != 1 {
				t.Fatal("dump conflates enforcement and validation", string(dump))
			}
			old, err := c.QueryContext(p.Ctx, consumer.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			labels, err := old.Columns()
			if err != nil {
				t.Fatal(err)
			}
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			enforcementSameDump(t, p, dump)
			check()
			if !reflect.DeepEqual(labels, []string{"a", "note"}) || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal("old metadata mutated", labels, types)
			}
			for _, want := range []struct {
				a    int64
				note string
			}{{0, "bad check"}, {9, "orphan"}} {
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var a int64
				var note string
				if err := old.Scan(&a, &note); err != nil || a != want.a || note != want.note {
					t.Fatal(a, note, err)
				}
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("old snapshot tail", old.Err())
			}
			old.Close()
			if nullable, known := types[0].Nullable(); !known || !nullable {
				t.Fatal("old nullable descriptor mutated", nullable, known)
			}
			run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
			run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
			before := enforcementSnapshot(t, p, b, c)
			bad := append(append([]byte(nil), before.dump...), []byte("ALTER TABLE t ADD CONSTRAINT late CHECK(missing>0) NOT ENFORCED;")...)
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			fixtErr := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad))
			fixture.AssertDiagnostic(t, fixtErr, &want)
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
		// Continued nonenforcement after replay, then normal binding/scanning.
		run(fixture.Exec("INSERT INTO t VALUES(-1,'still false')", 1))
		run(fixture.Exec("DELETE FROM t WHERE a=-1", 1))
		run(fixture.ErrorDiagnostic("ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED", enforcementCheck("t", "ck", true, "")))
		run(fixture.Exec("UPDATE t SET a=1 WHERE a=0", 1))
		run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED", 0))
		run(fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", addConstraintFK("t", "fk", "a", "9", "p")))
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(10,'bad')", addConstraintFK("t", "fk", "a", "10", "p")))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(0,'bad')", enforcementCheck("t", "ck", false, "0, bad")))
		run(fixture.ErrorDiagnostic("INSERT INTO pending VALUES(0)", enforcementCheck("pending", "pending_ck", false, "0")))
	})
}

func TestReferenceConstraintEnforcementFeatureSmoke(t *testing.T) {
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT ENFORCED)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(0)", 1))
		ck := enforcementIdentity(t, p, b, c, "t", "ck", "CHECK ((a > 0)) NOT ENFORCED", false, false)
		fk := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
		enforcementTriggers(t, p, b, c, fk, false)
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, "SELECT a FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
		check := addConstraintHeld(t, p, b, c, "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED")
		before := enforcementSnapshot(t, p, b, c)
		validateConstraintHeldRun(t, p, b, check, fixture.ErrorDiagnostic(check.SQL, enforcementCheck("t", "ck", true, "")))
		enforcementUnchanged(t, p, b, c, before)
		run(fixture.Exec("UPDATE t SET a=9", 1))
		validateConstraintHeldRun(t, p, b, check, fixture.Exec(check.SQL, 0))
		if enforcementIdentity(t, p, b, c, "t", "ck", "CHECK ((a > 0))", true, true) == ck {
			t.Fatal("successful replacement retained CHECK identity")
		}
		enable := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
		before = enforcementSnapshot(t, p, b, c)
		validateConstraintHeldRun(t, p, b, enable, fixture.ErrorDiagnostic(enable.SQL, addConstraintFK("t", "fk", "a", "9", "p")))
		enforcementUnchanged(t, p, b, c, before)
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		validateConstraintHeldRun(t, p, b, enable, fixture.Exec(enable.SQL, 0))
		if enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id)", true, true) != fk {
			t.Fatal("toggle replaced FK identity")
		}
		enforcementTriggers(t, p, b, c, fk, true)
		write := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1)")
		validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, addConstraintFK("t", "fk", "a", "10", "p"), 10))
		validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, enforcementCheck("t", "ck", false, "0"), 0))
		run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED", 0))
		validateConstraintHeldRun(t, p, b, write, fixture.Exec(write.SQL, 1, 10))
		run(fixture.Exec("CREATE TABLE false_check(a INT,CONSTRAINT false_ck CHECK(a>0) NOT ENFORCED)", 0))
		run(fixture.Exec("INSERT INTO false_check VALUES(0)", 1))
		if !b.PG {
			dump := addConstraintDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			enforcementSameDump(t, p, dump)
		}
		fk = enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
		enforcementTriggers(t, p, b, c, fk, false)
		run(enforcementFlags("false_check", "false_ck", false, false))
		run(fixture.Select("SELECT a FROM false_check", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO p VALUES(10)", 1))
		validateConstraintHeldRun(t, p, b, enable, fixture.Exec(enable.SQL, 0))
		validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, addConstraintFK("t", "fk", "a", "11", "p"), 11))
		run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
	})
}
