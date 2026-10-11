package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintTransactions(t *testing.T) {
	addConstraintTransactionLifecycle(t)
	t.Run("held_ddl_current_data_and_savepoints", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0)")
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO t VALUES(0,'late')", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, addConstraintCases()[0].build))
			run(fixture.Exec("DELETE FROM t WHERE a=0", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 0))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ck", 0))
			for _, commit := range []bool{false, true} {
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				trun(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0)", 0))
				trun(fixture.Exec("SAVEPOINT keep", 0))
				trun(fixture.ErrorDiagnostic("INSERT INTO t VALUES(0,'bad')", addConstraintCases()[0].write))
				trun(fixture.ErrorDiagnostic("SELECT a FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				trun(fixture.Exec("ROLLBACK TO keep", 0))
				trun(fixture.Exec("INSERT INTO t VALUES(2,'private')", 1))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					run(addConstraintRows([]any{int64(1), "old"}, []any{int64(2), "private"}))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(0,'bad')", addConstraintCases()[0].write))
				} else {
					run(addConstraintRows([]any{int64(1), "old"}))
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
				}
			}
		})
	})
	t.Run("mixed_late_failure_complete_candidate", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(1,'two')", 2))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT old CHECK(a>0)", 0))
			cat, rels := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
			var dump []byte
			if !b.PG {
				dump = addConstraintDump(t, p)
			}
			q := "ALTER TABLE t DROP CONSTRAINT old, ALTER a TYPE BIGINT, ADD extra INT DEFAULT 7, ADD CONSTRAINT new_ck CHECK(extra>0), ADD CONSTRAINT uq UNIQUE(a)"
			run(fixture.Prepared(fixture.ErrorDiagnostic(q, addConstraintCases()[2].build)))
			addConstraintUnchanged(t, p, b, c, cat, rels, dump)
			run(addConstraintRows([]any{int64(1), "one"}, []any{int64(1), "two"}))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, false, "SELECT a,note FROM t", []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
			run(fixture.Exec("UPDATE t SET a=2 WHERE note='two'", 1))
			run(fixture.Exec(q, 0))
			run(fixture.Select("SELECT a,note,extra FROM t ORDER BY a", []string{"a", "note", "extra"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(1), "one", int64(7)}, {int64(2), "two", int64(7)}}))
		})
	})
	for _, kind := range []string{"check", "not_null", "foreign_key"} {
		t.Run("invalid_type_"+kind, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
				name, clause, def := "ck", "CHECK(a>0)", "CHECK ((a > 0)) NOT VALID"
				old := any(int64(-1))
				if kind == "not_null" {
					name, clause, def, old = "nn", "NOT NULL a", "NOT NULL a NOT VALID", nil
				}
				if kind == "foreign_key" {
					name, clause, def, old = "fk", "FOREIGN KEY(a) REFERENCES p(id)", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID", int64(9)
				}
				run(fixture.Exec("INSERT INTO t VALUES($1,1)", 1, old))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+name+" "+clause+" NOT VALID", 0))
				oid := addConstraintInvalid(t, p, b, c, "t", name, def)
				run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER b TYPE BIGINT", 0)))
				if got := addConstraintInvalid(t, p, b, c, "t", name, def); got != oid {
					t.Fatal("unrelated TYPE rebuilt constraint", oid, got)
				}
				run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT8"}, [][]any{{old, int64(1)}}))
				run(fixture.Exec("ALTER TABLE t ALTER a TYPE BIGINT", 0))
				got := addConstraintInvalid(t, p, b, c, "t", name, def)
				if b.PG && got == oid {
					t.Fatal("affected TYPE retained old constraint OID", oid)
				}
				run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{old, int64(1)}}))
				var violation fixture.Diagnostic
				switch kind {
				case "check":
					violation = fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
					violation.Detail, violation.Schema, violation.Table, violation.Constraint = "Failing row contains (-2, 2).", "public", "t", "ck"
				case "not_null":
					violation = fixture.PrimaryKeyReferenceNull("t", "a", "null, 2")
				case "foreign_key":
					violation = addConstraintFK("t", "fk", "a", "3", "p")
					old = int64(3)
				}
				bad := old
				if kind == "check" {
					bad = int64(-2)
				}
				run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,2)", violation, bad)))
				if kind == "foreign_key" {
					run(fixture.Exec("ALTER TABLE p ALTER id TYPE BIGINT", 0))
					addConstraintInvalid(t, p, b, c, "t", name, def)
					before := addConstraintCatalog(t, p, c)
					d := fixture.ControlsReferenceDiagnostic("42804", `foreign key constraint "fk" cannot be implemented`)
					d.Detail = `Key columns "a" of the referencing table and "id" of the referenced table are of incompatible types: text and bigint.`
					addConstraintError(t, p, c, "ALTER TABLE t ALTER a TYPE TEXT", d, false, "execute")
					if fmt.Sprint(before) != fmt.Sprint(addConstraintCatalog(t, p, c)) {
						t.Fatal("incompatible TYPE changed FK")
					}
				}
			})
		})
	}
	t.Run("invalid_rewrite_using_and_valid_counterexample", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ck(a INT)", 0))
			run(fixture.Exec("INSERT INTO ck VALUES(1)", 1))
			run(fixture.Exec("ALTER TABLE ck ADD CONSTRAINT ck_invalid CHECK(a>0) NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE ck ALTER a TYPE BIGINT USING -a", 0))
			run(fixture.Select("SELECT a FROM ck", []string{"a"}, []string{"INT8"}, [][]any{{int64(-1)}}))
			run(fixture.Exec("CREATE TABLE nn(a INT)", 0))
			run(fixture.Exec("INSERT INTO nn VALUES(1)", 1))
			run(fixture.Exec("ALTER TABLE nn ADD CONSTRAINT nn_invalid NOT NULL a NOT VALID", 0))
			run(fixture.Exec("ALTER TABLE nn ALTER a TYPE BIGINT USING NULL::bigint", 0))
			run(fixture.Select("SELECT a FROM nn", []string{"a"}, []string{"INT8"}, [][]any{{nil}}))
			run(fixture.Exec("CREATE TABLE valid(a INT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO valid VALUES(1)", 1))
			d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "valid" contains null values`)
			d.Schema, d.Table, d.Column = "public", "valid", "a"
			run(fixture.ErrorDiagnostic("ALTER TABLE valid ALTER a TYPE BIGINT USING NULL::bigint", d))
			run(fixture.Select("SELECT a FROM valid", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
}
