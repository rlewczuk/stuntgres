package reference_test

import (
	"context"
	"testing"
)

func TestReferenceUnicodeEscapesCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
			run(referenceExec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\+01F600'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err := backend.db.ExecContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			lexicalOrdinary(t, err, context.Canceled)
			rows, err := backend.db.QueryContext(canceled, `SELECT * FROM U&"\0041"`)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled cursor")
			}
			lexicalOrdinary(t, err, context.Canceled)
			stmt, err := backend.db.PrepareContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled statement")
			}
			lexicalOrdinary(t, err, context.Canceled)
			for _, q := range []string{`INSERT INTO "A" VALUES (6,U&'\0041')`, `SELECT * FROM U&"\0041"`} {
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.ExecContext(canceled)
				lexicalOrdinary(t, err, context.Canceled)
				rows, err = stmt.QueryContext(canceled)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled prepared cursor")
				}
				lexicalOrdinary(t, err, context.Canceled)
				stmt.Close()
			}
			run(referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
		})
	}
}
