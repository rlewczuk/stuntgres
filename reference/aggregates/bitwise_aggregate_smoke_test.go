package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Reuse the same independent witnesses and actual consumers, not a smoke-only
// echo or a second set of weaker expectations. Replay controls first so the
// PostgreSQL phase can consume the actual native emitted catalog into public.
func TestReferenceBitwiseAggregateFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"bw_reader"})
	var emitted string
	bitwiseAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		bitwiseAggregateControls(t,p,b,&emitted)
		bitwiseAggregateValues(t,p,b)
		bitwiseAggregateMetadataCases(t,p,b)
		bitwiseAggregateBoundaries(t,p,b)
		bitwiseAggregateDiagnostics(t,p,b)
		bitwiseAggregateCallers(t,p,b)
	})
}
