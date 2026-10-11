package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\+01F600'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := backend.DB.ExecContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			fixture.LexicalOrdinary(t, err, context.Canceled)
			rows, err := backend.DB.QueryContext(canceled, `SELECT * FROM U&"\0041"`)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			stmt, err := backend.DB.PrepareContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled statement")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			for _, q := range []string{`INSERT INTO "A" VALUES (6,U&'\0041')`, `SELECT * FROM U&"\0041"`} {
				stmt, err := backend.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.ExecContext(canceled)
				fixture.LexicalOrdinary(t, err, context.Canceled)
				rows, err = stmt.QueryContext(canceled)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled prepared cursor")
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
				stmt.Close()
			}
			run(fixture.Select(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
		})
	}
}
