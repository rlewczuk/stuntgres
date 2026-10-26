package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func validateConstraintDetached(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, nullable bool, want [][]any) func() {
	t.Helper()
	if b.PG {
		return func() {}
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT a,note FROM t ORDER BY a,note")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"a", "note"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	return func() {
		t.Helper()
		if types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("validation changed detached types")
		}
		if got, known := types[0].Nullable(); !known || got != nullable {
			t.Fatal("validation changed detached nullability", got, known)
		}
		labels, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(labels, names) {
			t.Fatal("validation changed detached labels", labels, err)
		}
		for _, expected := range want {
			if !rows.Next() {
				t.Fatal("detached row missing", rows.Err())
			}
			var a, note any
			if err := rows.Scan(&a, &note); err != nil {
				t.Fatal(err)
			}
			got := []any{fixture.Normalize(a), fixture.Normalize(note)}
			if !reflect.DeepEqual(got, expected) {
				t.Fatal("detached row changed", got, expected)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("detached row tail", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func validateConstraintLogicalCases(t *testing.T) {
	t.Helper()
	t.Run("virtual_generated_not_null", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(dead TEXT,a INT,g INT GENERATED ALWAYS AS (NULLIF(a,0)) VIRTUAL)", 0))
			run(fixture.Exec("INSERT INTO t(dead,a) VALUES('discard',0)", 1))
			run(fixture.Exec("ALTER TABLE t DROP COLUMN dead", 0))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL g NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT nn")
			d := fixture.ControlsReferenceDiagnostic("23502", `column "g" of relation "t" contains null values`)
			d.Schema, d.Table, d.Column = "public", "t", "g"
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, d))
			run(validateConstraintFlag("t", "nn", false))
			run(fixture.Select("SELECT a,g FROM t", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(0), nil}}))
			run(fixture.Exec("UPDATE t SET a=1", 1))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(validateConstraintFlag("t", "nn", true))
			run(fixture.Select("SELECT a,g FROM t", []string{"a", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		})
	})
	t.Run("simple_partial_null", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
			run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(9,NULL),(NULL,9),(NULL,NULL)", 3))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH SIMPLE NOT VALID", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT fk", 0)))
			run(validateConstraintFlag("t", "fk", true))
			run(fixture.Select("SELECT a,b FROM t ORDER BY a NULLS LAST,b NULLS LAST", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), nil}, {nil, int64(9)}, {nil, nil}}))
		})
	})
	for _, target := range []string{"IF EXISTS public.t", "ONLY t", "t *"} {
		t.Run("target/"+target, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
				run(fixture.Prepared(fixture.Exec("ALTER TABLE "+target+" VALIDATE CONSTRAINT ck", 0)))
				run(validateConstraintFlag("t", "ck", true))
				run(addConstraintRows([]any{int64(1), "one"}))
			})
		})
	}
}

func validateConstraintValidChild(t *testing.T) {
	t.Helper()
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE child_scans", 0))
		run(fixture.Exec("CREATE TABLE root(a INT)", 0))
		run(fixture.Exec("CREATE TABLE child() INHERITS(root)", 0))
		run(fixture.Exec("INSERT INTO root VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO child VALUES(2)", 1))
		run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT ck CHECK(nextval('child_scans')>0)", 0))
		run(fixture.Select("SELECT last_value FROM child_scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ALTER TABLE root ADD CONSTRAINT ck CHECK(nextval('child_scans')>0) NOT VALID", 0))
		run(validateConstraintFlag("root", "ck", false))
		run(validateConstraintFlag("child", "ck", true))
		before := validateConstraintSnapshot(t, p, c)
		run(fixture.Prepared(fixture.Exec("ALTER TABLE root VALIDATE CONSTRAINT ck", 0)))
		run(validateConstraintFlag("root", "ck", true))
		run(validateConstraintFlag("child", "ck", true))
		run(fixture.Select("SELECT last_value FROM child_scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		validateConstraintOrigins(t, p, c, before)
	})
}
