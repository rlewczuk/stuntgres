package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedTemporaryPersistenceRejected(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			c := w.c
			exec("CREATE TEMP SEQUENCE s")
			var schema string
			if err := c.QueryRowContext(p.Ctx, "SELECT relnamespace::regnamespace::text FROM pg_class WHERE oid='s'::regclass").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"LOGGED", "UNLOGGED"} {
				_, err := c.ExecContext(p.Ctx, "ALTER SEQUENCE s SET "+action)
				want := fixture.ControlsReferenceDiagnostic("42P16", `cannot change logged status of table "s" because it is temporary`)
				want.Schema, want.Table = schema, "s"
				fixture.AssertDiagnostic(t, err, &want)
			}
		})
	}
}
