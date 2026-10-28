package fixture

import (
	"testing"
)

// These are independently specified contracts, not learned native goldens.
// Shared direct/prepared and full-diagnostic runners are the JSON fixture seams.
func JSONEachBoth(t *testing.T, p *Pair, b Backend, c Executor, s Step) {
	t.Helper()
	JSONKeysBoth(t, p, b, c, s)
}
