package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedTemporaryPersistenceRejected(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			c := w.c
			exec("CREATE TEMP SEQUENCE s")
			var schema string
			if err := c.QueryRowContext(p.ctx, "SELECT relnamespace::regnamespace::text FROM pg_class WHERE oid='s'::regclass").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"LOGGED", "UNLOGGED"} {
				_, err := c.ExecContext(p.ctx, "ALTER SEQUENCE s SET "+action)
				want := controlsReferenceDiagnostic("42P16", `cannot change logged status of table "s" because it is temporary`)
				want.Schema, want.Table = schema, "s"
				referenceAssertDiagnostic(t, err, &want)
			}
		})
	}
}
