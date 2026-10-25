package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGExtensionFeatureSmoke(t *testing.T) {
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgExtensionEmpty(t, p, b, "SELECT * FROM pg_catalog.pg_extension WHERE FALSE")
		pgExtensionClass(t, p, b)
		pgExtensionBoth(t, p, b, fixture.Select("SELECT 'pg_catalog.pg_extension'::regclass::oid AS id", []string{"id"}, []string{"OID"}, [][]any{{int64(3079)}}))
		oid := pgExtensionInstalled(t, p, b)
		stmt, err := b.DB.PrepareContext(p.Ctx, pgExtensionProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		pgExtensionHeld(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgExtensionProbe}, oid)
		pgExtensionMissing(t, p, b)
		pgExtensionProtected(t, p, b)
	})
	pgExtensionControlsScenario(t)
}
