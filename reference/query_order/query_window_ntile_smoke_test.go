package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryWindowNtileFeatureSmoke(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		queryWindowRoutes(t, p, b, queryWindowNtileLabels("SELECT id,ntile(b) OVER(ORDER BY id) AS n FROM nt WHERE id<=5 ORDER BY id", nil, int64(1), int64(1), int64(1), int64(2)))
		queryWindowRoutes(t, p, b, queryWindowNtileLabels("SELECT id,ntile(4) OVER(ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW) AS n FROM nt WHERE id<=6 ORDER BY id", int64(1), int64(1), int64(2), int64(2), int64(3), int64(4)))
		t.Run("same-held-recovery", func(t *testing.T) { queryWindowNtileHeld(t, p, b) })
		t.Run("late-partition-atomicity", func(t *testing.T) { queryWindowNtileAtomic(t, p, b) })
		t.Run("actual-view-replay-and-controls", func(t *testing.T) { queryWindowNtileViews(t, p, b) })
	})
}
