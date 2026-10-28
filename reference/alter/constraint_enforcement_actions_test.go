package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementActions(t *testing.T) {
	enforcementActionEdges(t)
	t.Run("check_skips_expression_preparation", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(10/a>0) NOT ENFORCED)", 0))
			held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1)")
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 1, 0))
			run(fixture.Prepared(fixture.Exec("UPDATE t SET a=0", 1)))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
			run(fixture.Exec("UPDATE t SET a=1", 1))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(10/a>0) ENFORCED", 0))
			before := enforcementSnapshot(t, p, b, c)
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("22012", "division by zero"), 0))
			enforcementUnchanged(t, p, b, c, before)
		})
	})
	for _, action := range []string{"CASCADE", "SET NULL"} {
		for _, operation := range []string{"UPDATE", "DELETE"} {
			t.Run(action+"/"+operation, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p ON UPDATE "+action+" ON DELETE "+action+" NOT ENFORCED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO t VALUES(1),(9)", 2))
					q := "UPDATE p SET id=2 WHERE id=1"
					if operation == "DELETE" {
						q = "DELETE FROM p WHERE id=1"
					}
					held := addConstraintHeld(t, p, b, c, q)
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 1))
					run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(9)}}))
					run(fixture.Exec("DELETE FROM p", map[string]int64{"UPDATE": 1, "DELETE": 0}[operation]))
					run(fixture.Exec("INSERT INTO p VALUES(1),(9)", 2))
					run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 1))
					rows := [][]any{{int64(2)}, {int64(9)}}
					if operation == "DELETE" {
						rows = [][]any{{int64(9)}}
					}
					if action == "SET NULL" {
						rows = [][]any{{int64(9)}, {nil}}
					}
					run(fixture.Select("SELECT a FROM t ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, rows))
				})
			})
		}
	}
	t.Run("multirow_atomic_and_trigger_modes", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
			run(fixture.Exec("INSERT INTO t VALUES(9,'before'),(10,'before')", 2))
			run(fixture.Exec("DELETE FROM t", 2))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk ENFORCED", 0))
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'good'),(9,'bad')", addConstraintFK("t", "fk", "a", "9", "p"))))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
			before = enforcementSnapshot(t, p, b, c)
			run(fixture.ErrorDiagnostic("UPDATE t SET a=9", addConstraintFK("t", "fk", "a", "9", "p")))
			enforcementUnchanged(t, p, b, c, before)
			run(fixture.Exec("ALTER TABLE t DISABLE TRIGGER ALL", 0))
			run(fixture.Exec("INSERT INTO t VALUES(9,'disabled')", 1))
			run(enforcementFlags("t", "fk", true, true))
			run(fixture.Exec("DELETE FROM t WHERE a=9", 1))
			run(fixture.Exec("ALTER TABLE t ENABLE TRIGGER ALL", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(9,'bad')", addConstraintFK("t", "fk", "a", "9", "p")))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED", 0))
			run(fixture.Exec("ALTER TABLE t ENABLE TRIGGER ALL", 0))
			run(fixture.Exec("INSERT INTO t VALUES(9,'absent')", 1))
		})
	})
}

func TestReferenceConstraintEnforcementPrepared(t *testing.T) {
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := addConstraintSetup(t, p, b, c)
		run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
		write := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
		read := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a,note")
		enable := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
		disable := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk NOT ENFORCED")
		run(enforcementFlags("t", "fk", false, false))
		validateConstraintHeldRun(t, p, b, write, fixture.Exec(write.SQL, 1, 9, "orphan"))
		before := enforcementSnapshot(t, p, b, c)
		validateConstraintHeldRun(t, p, b, enable, fixture.ErrorDiagnostic(enable.SQL, addConstraintFK("t", "fk", "a", "9", "p")))
		enforcementUnchanged(t, p, b, c, before)
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		validateConstraintHeldRun(t, p, b, enable, fixture.Exec(enable.SQL, 0))
		validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, addConstraintFK("t", "fk", "a", "10", "p"), 10, "bad"))
		validateConstraintHeldRun(t, p, b, read, addConstraintRows([]any{int64(9), "orphan"}))
		// Native cursors are detached snapshots; pq keeps locks until consumed.
		// The client-specific native-only lifetime phase is inside the shared body.
		var rows *sql.Rows
		var labels []string
		var types []*sql.ColumnType
		if !b.PG {
			var err error
			rows, err = read.QueryContext(p.Ctx, read.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			labels, err = rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			types, err = rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		validateConstraintHeldRun(t, p, b, disable, fixture.Exec(disable.SQL, 0))
		validateConstraintHeldRun(t, p, b, write, fixture.Exec(write.SQL, 1, 10, "new"))
		if !b.PG {
			if !reflect.DeepEqual(labels, []string{"a", "note"}) || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal("retained metadata changed", labels, types)
			}
			if nullable, known := types[0].Nullable(); !known || !nullable {
				t.Fatal("retained nullability", nullable, known)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var a int64
			var note string
			if err := rows.Scan(&a, &note); err != nil || a != 9 || note != "orphan" {
				t.Fatal(a, note, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained tail", rows.Err())
			}
			rows.Close()
		}
		validateConstraintHeldRun(t, p, b, read, addConstraintRows([]any{int64(9), "orphan"}, []any{int64(10), "new"}))
		validateConstraintHeldRun(t, p, b, enable, fixture.ErrorDiagnostic(enable.SQL, addConstraintFK("t", "fk", "a", "10", "p")))
		run(fixture.Exec("INSERT INTO p VALUES(10)", 1))
		validateConstraintHeldRun(t, p, b, enable, fixture.Exec(enable.SQL, 0))
		validateConstraintHeldRun(t, p, b, write, fixture.ErrorDiagnostic(write.SQL, addConstraintFK("t", "fk", "a", "11", "p"), 11, "bad"))
	})
}
