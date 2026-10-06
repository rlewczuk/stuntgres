//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequencePreparedInvalidation(t *testing.T) {
	t.Run("Rename", func(t *testing.T) {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(query, func(t *testing.T) {
				p := newReferencePair(t)
				for _, backend := range []struct {
					name string
					db   *sql.DB
				}{{"native", p.memory}, {"postgres", p.postgres}} {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
						held, err := c.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						sequenceProbeExec(t, p.ctx, c, "ALTER SEQUENCE s RENAME TO identity")
						var value int64
						err = held.QueryRowContext(p.ctx).Scan(&value)
						missing := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
						missing.Position = "16"
						referenceAssertDiagnostic(t, err, &missing)
						missingStatement, prepareErr := c.PrepareContext(p.ctx, query)
						if missingStatement != nil {
							missingStatement.Close()
							t.Fatal("missing name Prepare returned a statement")
						}
						referenceAssertDiagnostic(t, prepareErr, &missing)
						parameter, err := c.PrepareContext(p.ctx, "SELECT nextval($1)")
						if err != nil {
							t.Fatal(err)
						}
						defer parameter.Close()
						if err = parameter.QueryRowContext(p.ctx, "identity").Scan(&value); err != nil || value != 14 {
							t.Fatalf("parameter got=%d want=14 err=%v; failed held query must not allocate", value, err)
						}
						sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 70")
						if err = held.QueryRowContext(p.ctx).Scan(&value); err != nil || value != 70 {
							t.Fatal("invalidated source did not resolve alternate same-name sequence", value, err)
						}
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('identity')", 15)
					})
				}
			})
		}
	})
	referenceSequencePreparedMatrix(t)
	referenceSequencePreparedQueued(t)
	referenceSequencePreparedNamespace(t)
	referenceSequencePreparedDemand(t)
	referenceSequencePreparedQueuedArguments(t)
}
