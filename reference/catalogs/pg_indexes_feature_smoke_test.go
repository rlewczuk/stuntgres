package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGIndexesFeatureSmoke(t *testing.T) {
	t.Run("DefinitionsHeldLifecycleAndDiagnostics", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) { pgIndexesLifecycleScenario(t, p, b); pgIndexesDiagnosticsScenario(t, p, b) })
		}
	})
	t.Run("IndependentDisclosure", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgi_reader", "pgi_owner"})
		fixture.SchemaReferenceCleanup(t, "pgi_private")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) { pgIndexesVisibilityScenario(t, p, b) })
		}
	})
	t.Run("ActualDumpReplayAndExpressionHashMetadata", pgIndexesControlsScenario)
}
