package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
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
					tr := func(s fixture.Step) {
						s.Prepared = prepared
						if s.State != "" {
							s.ErrorStage = "execute"
							if prepared && s.State == "25P02" {
								s.ErrorStage = "prepare"
							}
						}
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
					}
					tr(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0))
					tr(fixture.ImplicitError("INSERT INTO t VALUES($1,$2)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad"))
					tr(fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
					run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
					tx, err = b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0))
					tr(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, 8, "valid"))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					tx, err = b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn", 0))
					tr(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, nil, "rolled_back"))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 2)
					run(fixture.ImplicitError("INSERT INTO t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}))
					tx, err = b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn", 0))
					tr(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, nil, "allowed"))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, false}, 3)
					run(fixture.Select("SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "allowed"}, {int64(7), "seed"}, {int64(8), "valid"}}))
				})
			}
		})
	}
}
