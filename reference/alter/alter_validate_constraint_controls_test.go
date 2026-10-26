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

func TestReferenceAlterValidateConstraintControls(t *testing.T) {
	addConstraintScenario(t, validateConstraintControls)
}

func validateConstraintControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := addConstraintSetup(t, p, b, c)
	run(fixture.Exec("INSERT INTO t VALUES(1,'keep'),(2,'other')", 2))
	run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID, ADD CONSTRAINT nn NOT NULL a NOT VALID, ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
	consumer := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a,note")
	before := validateConstraintSnapshot(t, p, c)
	held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT ck, VALIDATE CONSTRAINT nn, VALIDATE CONSTRAINT fk")
	validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
	validateConstraintOrigins(t, p, c, before)
	run(fixture.Exec("CREATE TABLE pending(a INT,b INT,n INT)", 0))
	run(fixture.Exec("INSERT INTO pending VALUES(9,0,NULL)", 1))
	run(fixture.Exec("ALTER TABLE pending ADD CONSTRAINT pending_ck CHECK(b>0) NOT VALID, ADD CONSTRAINT pending_nn NOT NULL n NOT VALID, ADD CONSTRAINT pending_fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
	check := func() {
		for _, name := range []string{"ck", "nn", "fk"} {
			run(validateConstraintFlag("t", name, true))
		}
		for _, name := range []string{"pending_ck", "pending_nn", "pending_fk"} {
			run(validateConstraintFlag("pending", name, false))
		}
		validateConstraintRows(t, p, b, c, false, []any{int64(1), "keep"}, []any{int64(2), "other"})
		validateConstraintHeldRun(t, p, b, consumer, addConstraintRows([]any{int64(1), "keep"}, []any{int64(2), "other"}))
		run(fixture.Select("SELECT a,b,n FROM pending", []string{"a", "b", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(0), nil}}))
		run(fixture.Select("SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname", []string{"conname", "definition"}, []string{"NAME", "TEXT"}, [][]any{{"ck", "CHECK ((a > 0))"}, {"fk", "FOREIGN KEY (a) REFERENCES p(id)"}, {"nn", "NOT NULL a"}}))
		run(fixture.Select("SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pending'::regclass ORDER BY conname", []string{"conname", "definition"}, []string{"NAME", "TEXT"}, [][]any{{"pending_ck", "CHECK ((b > 0)) NOT VALID"}, {"pending_fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID"}, {"pending_nn", "NOT NULL n NOT VALID"}}))
	}
	check()
	pendingBefore := validateConstraintSnapshot(t, p, c)
	pendingCheck := fixture.ControlsReferenceDiagnostic("23514", `check constraint "pending_ck" of relation "pending" is violated by some row`)
	pendingCheck.Schema, pendingCheck.Table, pendingCheck.Constraint = "public", "pending", "pending_ck"
	pendingNN := fixture.ControlsReferenceDiagnostic("23502", `column "n" of relation "pending" contains null values`)
	pendingNN.Schema, pendingNN.Table, pendingNN.Column = "public", "pending", "n"
	for _, failure := range []struct {
		name       string
		diagnostic fixture.Diagnostic
	}{
		{"pending_ck", pendingCheck},
		{"pending_nn", pendingNN},
		{"pending_fk", addConstraintFK("pending", "pending_fk", "a", "9", "p")},
	} {
		h := addConstraintHeld(t, p, b, c, "ALTER TABLE pending VALIDATE CONSTRAINT "+failure.name)
		validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(h.SQL, failure.diagnostic))
		if !reflect.DeepEqual(pendingBefore, validateConstraintSnapshot(t, p, c)) {
			t.Fatal("failed mixed validation changed state")
		}
	}
	if !b.PG {
		old := validateConstraintSnapshot(t, p, c)
		dump := addConstraintDump(t, p)
		if strings.Count(string(dump), "NOT VALID") != 3 {
			t.Fatal("dump lost mixed validity", string(dump))
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT a,note FROM t ORDER BY a")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		labels, err := retained.Columns()
		if err != nil || !reflect.DeepEqual(labels, []string{"a", "note"}) {
			t.Fatal(labels, err)
		}
		types, err := retained.ColumnTypes()
		if err != nil || len(types) != 2 {
			t.Fatal(types, err)
		}
		if types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("old descriptor types changed")
		}
		if nullable, known := types[0].Nullable(); !known || nullable {
			t.Fatal("old enforced NN metadata", nullable, known)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Error("SELECT * FROM t", "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump, addConstraintDump(t, p)) {
			t.Fatal("mixed validity canonical redump differs")
		}
		after := validateConstraintSnapshot(t, p, c)
		if len(old) != len(after) {
			t.Fatal(old, after)
		}
		oldIDs := map[string]int64{}
		for _, row := range old {
			oldIDs[row[1].(string)] = row[0].(int64)
		}
		reminted := false
		for _, row := range after {
			if row[0].(int64) <= 0 {
				t.Fatal("restore has nonpositive identity", row)
			}
			if row[0].(int64) != oldIDs[row[1].(string)] {
				reminted = true
			}
		}
		if !reminted {
			t.Fatal("restore retained the complete original identity inventory", old, after)
		}
		check()
		for _, want := range []struct {
			a    int64
			note string
		}{{1, "keep"}, {2, "other"}} {
			if !retained.Next() {
				t.Fatal("retained rows ended", retained.Err())
			}
			var a int64
			var note string
			if err := retained.Scan(&a, &note); err != nil || a != want.a || note != want.note {
				t.Fatal(a, note, err)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cursor tail", retained.Err())
		}
		retained.Close()
		if nullable, known := types[0].Nullable(); !known || nullable {
			t.Fatal("restore mutated old descriptor")
		}
		d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "pending_ck" of relation "pending" is violated by some row`)
		d.Schema, d.Table, d.Constraint = "public", "pending", "pending_ck"
		run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE pending VALIDATE CONSTRAINT pending_ck", d)))
		if !bytes.Equal(dump, addConstraintDump(t, p)) {
			t.Fatal("failed validation changed canonical dump")
		}
		run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		sentinel := addConstraintDump(t, p)
		ids := validateConstraintSnapshot(t, p, c)
		// Real late restore DDL failure, not an artificial reader failure or VALIDATE whitelist change.
		bad := string(sentinel) + "ALTER TABLE pending ADD CONSTRAINT late CHECK(missing>0) NOT VALID;"
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		if !bytes.Equal(sentinel, addConstraintDump(t, p)) || !reflect.DeepEqual(ids, validateConstraintSnapshot(t, p, c)) {
			t.Fatal("late restore failure changed sentinel state")
		}
		run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		check()
	}
	ck := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
	ck.Detail, ck.Schema, ck.Table, ck.Constraint = "Failing row contains (0, bad).", "public", "t", "ck"
	write := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,'bad')")
	validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, ck, 0))
	validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, fixture.PrimaryKeyReferenceNull("t", "a", "null, bad"), nil))
	validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, addConstraintFK("t", "fk", "a", "9", "p"), 9))
	// Still-invalid definitions must enforce new rows after restoration too.
	run(fixture.ErrorDiagnostic("INSERT INTO pending VALUES(1,1,NULL)", fixture.PrimaryKeyReferenceNull("pending", "n", "1, 1, null")))
	run(fixture.ErrorDiagnostic("INSERT INTO pending VALUES(9,1,1)", addConstraintFK("pending", "pending_fk", "a", "9", "p")))
	check()
}

func TestReferenceAlterValidateConstraintFeatureSmoke(t *testing.T) {
	for _, tc := range validateConstraintCases() {
		t.Run(tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES($1,'bad')", 1, tc.bad))
				run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
				n, def := validateConstraintName(tc), validateConstraintDefinition(tc)
				oid := addConstraintInvalid(t, p, b, c, "t", n, def+" NOT VALID")
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT "+n)
				write := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,'bad')")
				run(fixture.ErrorDiagnostic(held.SQL, tc.build))
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, tc.build))
				validateConstraintIdentity(t, p, b, c, oid, false, def+" NOT VALID")
				run(fixture.Exec("UPDATE t SET a=1", 1))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				validateConstraintIdentity(t, p, b, c, oid, true, def)
				before := validateConstraintSnapshot(t, p, c)
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) {
					t.Fatal("repeat changed identity")
				}
				validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, tc.write, tc.bad))
				validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "bad"})
				if tc.kind == "c" {
					run(fixture.Exec("CREATE SEQUENCE smoke_scans", 0))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT counted CHECK(nextval('smoke_scans')>0) NOT VALID", 0))
					counted := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT counted")
					validateConstraintHeldRun(t, p, b, counted, fixture.Exec(counted.SQL, 0))
					run(fixture.Select("SELECT last_value FROM smoke_scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
					validateConstraintHeldRun(t, p, b, counted, fixture.Exec(counted.SQL, 0))
					run(fixture.Select("SELECT last_value FROM smoke_scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
				if !b.PG {
					dump := addConstraintDump(t, p)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump, addConstraintDump(t, p)) {
						t.Fatal("smoke canonical roundtrip")
					}
					run(validateConstraintFlag("t", n, true))
					validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, tc.write, tc.bad))
					validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "bad"})
				}
			})
		})
	}
	// The same smoke selector also exercises mixed valid/invalid native persistence.
	t.Run("mixed_persistence", func(t *testing.T) { addConstraintScenario(t, validateConstraintControls) })
}
