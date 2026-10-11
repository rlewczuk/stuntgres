package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultDestinationTypes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec(`CREATE TABLE types(s SMALLINT DEFAULT 32767,l BIGINT DEFAULT 2147483648,u UUID DEFAULT '550e8400-e29b-41d4-a716-446655440000',r REAL DEFAULT 1.25,f DOUBLE PRECISION DEFAULT 2.5,n NUMERIC DEFAULT 'NaN')`, 0))
			run(fixture.Exec(`INSERT INTO types DEFAULT VALUES`, 1))
			run(fixture.Select(`SELECT * FROM types`, []string{"s", "l", "u", "r", "f", "n"}, []string{"INT2", "INT8", "UUID", "FLOAT4", "FLOAT8", "NUMERIC"}, [][]any{{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"}}))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE types"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL replay", err)
				}
			}
			run(fixture.Exec(`INSERT INTO types DEFAULT VALUES`, 1))
			run(fixture.Select(`SELECT * FROM types`, []string{"s", "l", "u", "r", "f", "n"}, []string{"INT2", "INT8", "UUID", "FLOAT4", "FLOAT8", "NUMERIC"}, [][]any{
				{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"},
				{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"},
			}))
		})
	}
}

func TestReferenceDefaultTransactionFailureAndExplicitBypass(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE d(id INT,v INT DEFAULT 2147483648)`, 0))
			run(fixture.Exec(`INSERT INTO d VALUES(1,9)`, 1))
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(`INSERT INTO d VALUES(2,8)`, 1))
			failure := fixture.ImplicitError(`INSERT INTO d(id) VALUES(3),(4)`, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, failure)
			_, err = tx.ExecContext(p.Ctx, `SELECT * FROM d`)
			if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "25P02" {
				t.Fatalf("aborted transaction: %T %v", err, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT * FROM d`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		})
	}
}
