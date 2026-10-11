package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: backend.Recorder}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			db := backend.DB
			db.SetMaxOpenConns(3)
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			for _, prepared := range []bool{false, true} {
				if err := m.TransactionVisibility(ctx, db, a, b, prepared); err != nil {
					t.Fatal(err)
				}
				if err := m.TransactionFailures(ctx, db, a, b, prepared); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.TransactionControls(ctx, db, a, b); err != nil {
				t.Fatal(err)
			}
			if err := m.TransactionCancellation(ctx, db, a, b); err != nil {
				t.Fatal(err)
			}
		})
	}
}
