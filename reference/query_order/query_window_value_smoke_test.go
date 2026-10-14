package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryWindowValueFeatureSmoke(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowValueSeed(t, p, b)
		t.Run("default", func(t *testing.T) { queryWindowValueDefault(t, p, b) })
		t.Run("whole", func(t *testing.T) { queryWindowValueWhole(t, p, b) })
		t.Run("discontiguous", func(t *testing.T) { queryWindowValueExcluded(t, p, b) })
		t.Run("held-error-recovery", func(t *testing.T) { queryWindowValueHeld(t, p, b) })
		t.Run("static-invalid-frame", func(t *testing.T) { queryWindowValueInvalidFrame(t, p, b) })
		t.Run("empty-and-nonempty-metadata", func(t *testing.T) { queryWindowValueMetadata(t, p, b) })
		t.Run("volatile-call-and-upper-target-identity", func(t *testing.T) { queryWindowValueVolatile(t, p, b) })
		t.Run("persisted-view-and-native-controls", func(t *testing.T) { queryWindowValueViews(t, p, b) })
	})
}
