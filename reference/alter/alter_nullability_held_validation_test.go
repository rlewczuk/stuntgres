package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityHeldValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					utility := func(q string, count int64) { s := fixture.Exec(q, count); s.Prepared = prepared; run(s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(NULL,'old')", 1))
					utility("UPDATE t SET a=7", 1)
					selectQuery := "SELECT * FROM t ORDER BY b"
					selected, err := b.DB.PrepareContext(p.Ctx, selectQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer selected.Close()
					insertQuery := "INSERT INTO t VALUES($1,$2)"
					insert, err := b.DB.PrepareContext(p.Ctx, insertQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer insert.Close()
					updateQuery := "UPDATE t SET a=$1 WHERE b=$2"
					update, err := b.DB.PrepareContext(p.Ctx, updateQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer update.Close()
					heldSelect := fixture.HeldStatement{Executor: b.DB, Statement: selected, SQL: selectQuery}
					heldInsert := fixture.HeldStatement{Executor: b.DB, Statement: insert, SQL: insertQuery}
					heldUpdate := fixture.HeldStatement{Executor: b.DB, Statement: update, SQL: updateQuery}
					check := func(nullable []bool, want [][]any) {
						fixture.TableDDLMetadata(t, p.Ctx, heldSelect, b.PG, false, selectQuery, []string{"a", "b"}, []string{"INT4", "TEXT"}, nullable, len(want))
						fixture.RunStep(t, p.Ctx, heldSelect, b.Recorder, fixture.Select(selectQuery, []string{"a", "b"}, []string{"INT4", "TEXT"}, want))
					}
					check([]bool{true, true}, [][]any{{int64(7), "old"}})
					utility("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0)
					check([]bool{false, false}, [][]any{{int64(7), "old"}})
					for _, probe := range []struct {
						executor     fixture.Executor
						query, value string
					}{{heldInsert, insertQuery, "bad"}, {heldUpdate, updateQuery, "old"}} {
						s := fixture.ImplicitError(probe.query, fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, " + probe.value + ").", Schema: "public", Table: "t", Column: "a"}, nil, probe.value)
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, probe.executor, b.Recorder, s)
					}
					s := fixture.ImplicitError("UPDATE t SET a=$1,b=$2", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad")
					s.Prepared, s.ErrorStage = true, "execute"
					run(s)
					check([]bool{false, false}, [][]any{{int64(7), "old"}})
					utility("ALTER TABLE t DROP CONSTRAINT nn, ALTER b DROP NOT NULL", 0)
					fixture.RunStep(t, p.Ctx, heldInsert, b.Recorder, fixture.Exec(insertQuery, 1, nil, "after"))
					check([]bool{true, true}, [][]any{{nil, "after"}, {int64(7), "old"}})
					utility("DELETE FROM t WHERE b='after'", 1)
					utility("ALTER TABLE t ALTER a SET NOT NULL", 0)
					run(fixture.ImplicitError("INSERT INTO t SELECT NULL,'query'", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, query).", Schema: "public", Table: "t", Column: "a"}))
				})
			}
		})
	}
}
