package reference_test

import (
	"context"
	"testing"
)

func TestReferenceDateNotNullTransaction(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE nn(v DATE NOT NULL)", 0))
			run(referenceImplicitError("INSERT INTO nn VALUES('epoch'),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			query := "SELECT v FROM nn"
			rows, err := b.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 {
				t.Fatal(meta, err)
			}
			nullable, known := meta[0].Nullable()
			if b.pg {
				if known {
					t.Fatal("pq nullable must be unknown")
				}
			} else if !known || nullable {
				t.Fatal("native NOT NULL metadata", meta[0])
			}
			s := referenceSelect(query, []string{"v"}, []string{"DATE"}, nil)
			referenceDateConsume(t, rows, b.pg, s)
			referenceDateRecorded(t, b.recorder, b.pg, s)
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceDateRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("INSERT INTO nn VALUES('bad')", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "23"}))
			referenceDateRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("INSERT INTO nn VALUES('epoch')", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(s)
		})
	}
}
