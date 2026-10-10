package reference_test

import "testing"

func TestReferencePGIndexesFeatureSmoke(t *testing.T) {
	t.Run("DefinitionsHeldLifecycleAndDiagnostics", func(t *testing.T) {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name, func(t *testing.T) { pgIndexesLifecycleScenario(t, p, b); pgIndexesDiagnosticsScenario(t, p, b) })
		}
	})
	t.Run("IndependentDisclosure", func(t *testing.T) {
		roleReferenceCleanup(t, []string{"pgi_reader", "pgi_owner"})
		schemaReferenceCleanup(t, "pgi_private")
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name, func(t *testing.T) { pgIndexesVisibilityScenario(t, p, b) })
		}
	})
	t.Run("ActualDumpReplayAndExpressionHashMetadata", pgIndexesControlsScenario)
}
