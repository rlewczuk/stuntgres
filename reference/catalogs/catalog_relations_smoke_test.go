package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCatalogRelationsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cr_held_role", "cr_held_rollback", "cr_lifecycle_role"})
	p := fixture.NewScenario(t)
	var emitted []byte
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) {
		// Reuse the independent release bag and real lifecycle consumers, not a
		// second implementation or a baseline connectivity/helper-only smoke.
		catalogRelationsInventory(t, p, b)
		s := fixture.Select("SELECT * FROM pg_get_catalog_foreign_keys() WHERE false", catalogRelationsColumns, catalogRelationsTypes, nil)
		s.CheckMetadata = fixture.CatalogRelationsMetadata(b.PG)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT pg_get_userbyid(0) AS missing,pg_get_userbyid(NULL::oid) AS absent", []string{"missing", "absent"}, []string{"NAME", "NAME"}, [][]any{{"unknown (OID=0)", nil}}))
		catalogRelationsHeld(t, p, b)
		emitted = catalogRelationsLifecycle(t, p, b, emitted)
	})
}
