package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGTSConfigMapFeatureSmoke(t *testing.T) {
	pgTSConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgTSConfigBoth(t, p, b, b.DB, pgTSConfigMapStep(b, pgTSConfigMapProbe, pgTSConfigMapStock(), int64(3748)))
		pgTSConfigMapReferences(t, p, b, b.DB)
		pgTSConfigMapHeld(t, p, b, pgTSConfigMapProbe, []any{int64(3748), int64(0), nil, int64(3748)}, int64(3748), pgTSConfigMapStock())
		pgTSConfigMapMissing(t, p, b)
		pgTSConfigMapLifecycle(t, p, b)
	})
}
