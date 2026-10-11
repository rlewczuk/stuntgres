package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDateNotNullTransaction(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE nn(v DATE NOT NULL)", 0))
			run(fixture.ImplicitError("INSERT INTO nn VALUES('epoch'),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"}))
			query := "SELECT v FROM nn"
			rows, err := b.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 {
				t.Fatal(meta, err)
			}
			nullable, known := meta[0].Nullable()
			if b.PG {
				if known {
					t.Fatal("pq nullable must be unknown")
				}
			} else if !known || nullable {
				t.Fatal("native NOT NULL metadata", meta[0])
			}
			s := fixture.Select(query, []string{"v"}, []string{"DATE"}, nil)
			fixture.DateConsume(t, rows, b.PG, s)
			fixture.DateRecorded(t, b.Recorder, b.PG, s)
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.DateRunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("INSERT INTO nn VALUES('bad')", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "23"}))
			fixture.DateRunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("INSERT INTO nn VALUES('epoch')", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(s)
		})
	}
}
