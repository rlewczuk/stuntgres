package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(NULL,'old')", 1))
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				p.Step(t, s)
			}

		})
	}
}

func TestReferenceAlterNullabilityNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,9)", 1))
			step := fixture.ImplicitError("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ADD CONSTRAINT other NOT NULL a", fixture.Diagnostic{Code: "55000", Message: `cannot create not-null constraint "other" on column "a" of table "t"`, Detail: `A not-null constraint named "nn" already exists for this column.`})
			step.ErrorStage = "execute"
			step.Prepared = prepared
			p.Step(t, step)
			p.Step(t, fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT other", fixture.Diagnostic{Code: "42704", Message: `constraint "other" of relation "t" does not exist`}))
			p.Step(t, fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT nn", fixture.Diagnostic{Code: "42704", Message: `constraint "nn" of relation "t" does not exist`}))
			p.Step(t, fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", 0))
			p.Step(t, fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn, ADD CONSTRAINT nn NOT NULL a", 0))
			step = fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn CASCADE, ADD CONSTRAINT nn NOT NULL b", 0)
			step.Prepared = prepared
			p.Step(t, step)
			step = fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL, ADD CONSTRAINT nn NOT NULL a", fixture.Diagnostic{Code: "55000", Message: `cannot create not-null constraint "nn" on column "a" of table "t"`, Detail: `A not-null constraint named "t_a_not_null" already exists for this column.`})
			step.Prepared = prepared
			step.ErrorStage = "execute"
			p.Step(t, step)
			// The failed transformed ADD rolls back the preceding SET identity.
			p.Step(t, fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT t_a_not_null", fixture.Diagnostic{Code: "42704", Message: `constraint "t_a_not_null" of relation "t" does not exist`}))
			p.Step(t, fixture.ImplicitError("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", fixture.Diagnostic{Code: "42710", Message: `constraint "nn" for relation "t" already exists`}))
			step = fixture.Exec("INSERT INTO t VALUES(NULL,10)", 1)
			step.Prepared = prepared
			p.Step(t, step)
			p.Step(t, fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn RESTRICT", 0))
		})
	}
}

func TestReferenceAlterNullabilityPreparedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,'seed')", 1))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			selectStmt, err := tx.PrepareContext(p.Ctx, "SELECT * FROM t ORDER BY b")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			fixture.NotNullRetainedRows(t, context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), selectStmt, !b.PG, true, [][]any{{int64(7), "seed"}})
			utilityQuery := "ALTER TABLE t ALTER a SET NOT NULL"
			utility, err := tx.PrepareContext(p.Ctx, utilityQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer utility.Close()
			heldUtility := fixture.HeldStatement{Executor: tx, Statement: utility, SQL: utilityQuery}
			fixture.RunStep(t, p.Ctx, heldUtility, b.Recorder, fixture.Exec(utilityQuery, 0))
			fixture.NotNullRetainedRows(t, context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), selectStmt, !b.PG, false, [][]any{{int64(7), "seed"}})
			tr(fixture.Exec("SAVEPOINT child", 0))
			tr(fixture.Exec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(NULL,'child')", 1))
			fixture.RunStep(t, p.Ctx, heldUtility, b.Recorder, fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			tr(fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			tr(fixture.Exec("ROLLBACK TO child", 0))
			fixture.NotNullRetainedRows(t, context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), selectStmt, !b.PG, false, [][]any{{int64(7), "seed"}})
			tr(fixture.Exec("RELEASE child", 0))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
			run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'after')", 1))
		})
	}
}

func TestReferenceAlterNullabilityOwnDeletesAndSequences(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,1),(7,2)", 2))
			run(fixture.Exec("CREATE SEQUENCE seq START 10", 0))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Exec("DELETE FROM t WHERE b=1", 1))
			tr(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(fixture.Exec("ALTER SEQUENCE seq RESTART WITH 20", 0))
			tr(fixture.Exec("SAVEPOINT child", 0))
			tr(fixture.Exec("ALTER TABLE t DROP b", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(8)", 1))
			tr(fixture.Exec("ROLLBACK TO child", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(9,3)", 1))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}, {int64(9), int64(3)}}))
			run(fixture.Select("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}))
			tx, err = b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr(fixture.Exec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(NULL,4)", 1))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.ImplicitError("INSERT INTO t VALUES(NULL,4)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 4).", Schema: "public", Table: "t", Column: "a"}))
			tx, err = b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr(fixture.Exec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(fixture.Exec("INSERT INTO t VALUES(NULL,4)", 1))
			tr(fixture.ImplicitError("ALTER TABLE t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
