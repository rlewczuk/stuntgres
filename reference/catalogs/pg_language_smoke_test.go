package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGLanguageFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pglang_reader"})
	p := fixture.NewScenario(t)
	dump := pgLanguageDump(t, p)
	pgLanguageBackends(t, p, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgLanguageInventory(t, p, b)
		pgLanguageHeldReads(t, p, b, b.DB)
		pgLanguagePermissions(t, p, b)
		pgLanguageCatalogLinks(t, p, b)
		pgLanguageMissing(t, p, b)
		pgLanguageProtected(t, p, b)
		pgLanguageLifecycle(t, p, b, dump)
	})
}
