package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityReviewProjectedUpdate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,9,11)", 1))
					tx, err := backend.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s) }
					retained, err := tx.PrepareContext(p.Ctx, "UPDATE t SET c=$1")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					update := func(n int) {
						t.Helper()
						step := fixture.Exec("UPDATE t SET c=$1", 1, n)
						if prepared {
							held := fixture.HeldStatement{Executor: tx, Statement: retained, SQL: step.Query}
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
						} else {
							tr(step)
						}
					}
					tr(fixture.Exec("SAVEPOINT before_shape", 0))
					tr(fixture.Exec("ALTER TABLE t DROP b, ALTER c SET NOT NULL", 0))
					update(12)
					tr(fixture.Exec("SAVEPOINT middle", 0))
					tr(fixture.Exec("ALTER TABLE t DROP a", 0))
					update(13)
					tr(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"INT4"}, [][]any{{int64(13)}}))
					tr(fixture.Exec("ROLLBACK TO middle", 0))
					update(14)
					tr(fixture.Exec("ROLLBACK TO before_shape", 0))
					update(15)
					tr(fixture.Exec("ALTER TABLE t DROP a, DROP b, ALTER c SET NOT NULL", 0))
					update(16)
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT * FROM t", []string{"c"}, []string{"INT4"}, [][]any{{int64(16)}}))
				})
			}
		})
	}
}

func TestReferenceAlterNullabilityReviewSuccessiveDependencies(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,9,11)", 1))
					run(fixture.Exec("CREATE SEQUENCE owned OWNED BY t.b", 0))
					run(fixture.Exec("CREATE VIEW v AS SELECT b,c FROM t", 0))
					run(fixture.Exec("CREATE VIEW w AS SELECT * FROM v", 0))
					if reverse {
						run(fixture.ImplicitError("ALTER TABLE t DROP c RESTRICT, DROP b CASCADE, ALTER a SET NOT NULL", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view v depends on column c of table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
						run(fixture.Select("SELECT * FROM w", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(11)}}))
					} else {
						run(fixture.Exec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0))
						run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
				})
			}
		})
	}
}

func TestReferenceAlterNullabilityReviewFirstRRSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,c INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,'keep',9)", 1))
					tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s) }
					tr(fixture.Exec("SAVEPOINT before_shape", 0))
					tr(fixture.Exec("ALTER TABLE t DROP a, ALTER c SET NOT NULL", 0))
					tr(fixture.Select("SELECT b,c FROM t", []string{"b", "c"}, []string{"TEXT", "INT4"}, [][]any{{"keep", int64(9)}}))
					tr(fixture.Exec("INSERT INTO t VALUES('own',10)", 1))
					tr(fixture.Exec("ALTER TABLE t DROP b", 0))
					tr(fixture.Select("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
					tr(fixture.Exec("ROLLBACK TO before_shape", 0))
					tr(fixture.Exec("INSERT INTO t VALUES(8,'restored',11)", 1))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}, {int64(8), "restored", int64(11)}}))
				})
			}
		})
	}
}
