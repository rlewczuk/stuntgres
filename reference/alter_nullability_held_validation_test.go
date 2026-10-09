package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityHeldValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					utility := func(q string, count int64) { s := referenceExec(q, count); s.prepared = prepared; run(s) }
					run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(NULL,'old')", 1))
					utility("UPDATE t SET a=7", 1)
					selectQuery := "SELECT * FROM t ORDER BY b"
					selected, err := b.db.PrepareContext(p.ctx, selectQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer selected.Close()
					insertQuery := "INSERT INTO t VALUES($1,$2)"
					insert, err := b.db.PrepareContext(p.ctx, insertQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer insert.Close()
					updateQuery := "UPDATE t SET a=$1 WHERE b=$2"
					update, err := b.db.PrepareContext(p.ctx, updateQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer update.Close()
					heldSelect := referenceHeldStatement{referenceExecutor: b.db, statement: selected, query: selectQuery}
					heldInsert := referenceHeldStatement{referenceExecutor: b.db, statement: insert, query: insertQuery}
					heldUpdate := referenceHeldStatement{referenceExecutor: b.db, statement: update, query: updateQuery}
					check := func(nullable []bool, want [][]any) {
						referenceTableDDLMetadata(t, p.ctx, heldSelect, b.pg, false, selectQuery, []string{"a", "b"}, []string{"INT4", "TEXT"}, nullable, len(want))
						referenceRunStep(t, p.ctx, heldSelect, b.recorder, referenceSelect(selectQuery, []string{"a", "b"}, []string{"INT4", "TEXT"}, want))
					}
					check([]bool{true, true}, [][]any{{int64(7), "old"}})
					utility("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0)
					check([]bool{false, false}, [][]any{{int64(7), "old"}})
					for _, probe := range []struct {
						executor     referenceExecutor
						query, value string
					}{{heldInsert, insertQuery, "bad"}, {heldUpdate, updateQuery, "old"}} {
						s := referenceImplicitError(probe.query, referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, " + probe.value + ").", Schema: "public", Table: "t", Column: "a"}, nil, probe.value)
						s.errorStage = "execute"
						referenceRunStep(t, p.ctx, probe.executor, b.recorder, s)
					}
					s := referenceImplicitError("UPDATE t SET a=$1,b=$2", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad")
					s.prepared, s.errorStage = true, "execute"
					run(s)
					check([]bool{false, false}, [][]any{{int64(7), "old"}})
					utility("ALTER TABLE t DROP CONSTRAINT nn, ALTER b DROP NOT NULL", 0)
					referenceRunStep(t, p.ctx, heldInsert, b.recorder, referenceExec(insertQuery, 1, nil, "after"))
					check([]bool{true, true}, [][]any{{nil, "after"}, {int64(7), "old"}})
					utility("DELETE FROM t WHERE b='after'", 1)
					utility("ALTER TABLE t ALTER a SET NOT NULL", 0)
					run(referenceImplicitError("INSERT INTO t SELECT NULL,'query'", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, query).", Schema: "public", Table: "t", Column: "a"}))
				})
			}
		})
	}
}
