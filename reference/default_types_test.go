package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceDefaultDestinationTypes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec(`CREATE TABLE types(s SMALLINT DEFAULT 32767,l BIGINT DEFAULT 2147483648,u UUID DEFAULT '550e8400-e29b-41d4-a716-446655440000',r REAL DEFAULT 1.25,f DOUBLE PRECISION DEFAULT 2.5,n NUMERIC DEFAULT 'NaN')`, 0))
			run(referenceExec(`INSERT INTO types DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT * FROM types`, []string{"s", "l", "u", "r", "f", "n"}, []string{"INT2", "INT8", "UUID", "FLOAT4", "FLOAT8", "NUMERIC"}, [][]any{{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"}}))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE types"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL replay", err)
				}
			}
			run(referenceExec(`INSERT INTO types DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT * FROM types`, []string{"s", "l", "u", "r", "f", "n"}, []string{"INT2", "INT8", "UUID", "FLOAT4", "FLOAT8", "NUMERIC"}, [][]any{
				{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"},
				{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"},
			}))
		})
	}
}

func TestReferenceDefaultTransactionFailureAndExplicitBypass(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec(`CREATE TABLE d(id INT,v INT DEFAULT 2147483648)`, 0))
			run(referenceExec(`INSERT INTO d VALUES(1,9)`, 1))
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec(`INSERT INTO d VALUES(2,8)`, 1))
			failure := referenceImplicitError(`INSERT INTO d(id) VALUES(3),(4)`, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, backend.recorder, failure)
			_, err = tx.ExecContext(p.ctx, `SELECT * FROM d`)
			if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "25P02" {
				t.Fatalf("aborted transaction: %T %v", err, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect(`SELECT * FROM d`, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
		})
	}
}
