package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintNotValid(t *testing.T) {
	addConstraintConsumers(t)
	for _, kind := range addConstraintCases() {
		if kind.kind != "c" && kind.kind != "n" && kind.kind != "f" {
			continue
		}
		t.Run(kind.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES($1,'old')", 1, kind.bad))
				held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
				run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD "+kind.clause+" NOT VALID", 0)))
				name, definition := "ck", "CHECK ((a > 0)) NOT VALID"
				if kind.kind == "n" {
					name, definition = "nn", "NOT NULL a NOT VALID"
				}
				if kind.kind == "f" {
					name, definition = "fk", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID"
				}
				oid := addConstraintInvalid(t, p, b, c, "t", name, definition)
				run(fixture.Select("SELECT a,note FROM t", []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{kind.bad, "old"}}))
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, false, "SELECT a,note FROM t", []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{kind.kind != "n", true}, 1)
				for _, heldWrite := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad")
					if heldWrite {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					} else {
						run(s)
					}
				}
				if kind.kind != "f" {
					d := kind.write
					d.Detail = "Failing row contains (" + map[string]string{"c": "0", "n": "null"}[kind.kind] + ", touched)."
					run(fixture.ErrorDiagnostic("UPDATE t SET note='touched'", d))
				} else {
					// Committed old orphan: unchanged equal key and unrelated note skip RI check.
					run(fixture.Exec("UPDATE t SET note='touched'", 1))
					run(fixture.Prepared(fixture.Exec("UPDATE t SET a=9", 1)))
					run(fixture.ErrorDiagnostic("UPDATE t SET a=10", addConstraintFK("t", "fk", "a", "10", "p")))
				}
				run(fixture.Exec("UPDATE t SET a=1", 1))
				if got := addConstraintInvalid(t, p, b, c, "t", name, definition); got != oid {
					t.Fatal("repair changed constraint identity", oid, got)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 2, "new"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, kind.write, kind.bad, "bad"))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 3, "last"))
				run(addConstraintRows([]any{int64(1), map[bool]string{true: "touched", false: "old"}[kind.kind == "f"]}, []any{int64(2), "new"}, []any{int64(3), "last"}))
				if kind.kind == "f" {
					d := fixture.ControlsReferenceDiagnostic("23503", `update or delete on table "p" violates foreign key constraint "fk" on table "t"`)
					d.Detail, d.Schema, d.Table, d.Constraint = `Key (id)=(1) is still referenced from table "t".`, "public", "t", "fk"
					run(fixture.ErrorDiagnostic("DELETE FROM p WHERE id=1", d))
				}
			})
		})
	}
	t.Run("full_partial_null_and_transaction_born", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
			run(fixture.Exec("CREATE TABLE c(a INT,b INT,note TEXT)", 0))
			run(fixture.Exec("INSERT INTO c VALUES(1,NULL,'partial'),(NULL,NULL,'empty'),(9,9,'orphan')", 3))
			run(fixture.Exec("ALTER TABLE c ADD CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH FULL NOT VALID", 0))
			d := addConstraintFK("c", "fk", "a, b", "1, null", "p")
			d.Detail = "MATCH FULL does not allow mixing of null and nonnull key values."
			run(fixture.ErrorDiagnostic("UPDATE c SET note='bad' WHERE note='partial'", d))
			run(fixture.Exec("UPDATE c SET note='empty2' WHERE note='empty'", 1))
			run(fixture.Exec("UPDATE c SET a=9,b=9 WHERE note='orphan'", 1))
			run(fixture.Exec("CREATE TABLE single_parent(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("INSERT INTO single_parent VALUES(1)", 1))
			run(fixture.Exec("CREATE TABLE born(pid INT,note TEXT)", 0))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			trun(fixture.Exec("INSERT INTO born VALUES(9,'new')", 1))
			trun(fixture.Exec("ALTER TABLE born ADD CONSTRAINT born_fk FOREIGN KEY(pid) REFERENCES single_parent NOT VALID", 0))
			trun(fixture.Exec("SAVEPOINT before_update", 0))
			trun(fixture.ErrorDiagnostic("UPDATE born SET note='changed'", addConstraintFK("born", "born_fk", "pid", "9", "single_parent")))
			trun(fixture.Exec("ROLLBACK TO before_update", 0))
			trun(fixture.Exec("UPDATE born SET pid=1", 1))
			trun(fixture.Exec("UPDATE born SET note='repaired'", 1))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT pid,note FROM born", []string{"pid", "note"}, []string{"INT4", "TEXT"}, nil))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='born'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		})
	})
}
