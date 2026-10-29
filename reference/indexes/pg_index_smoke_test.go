package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGIndexFeatureSmoke(t *testing.T) {
	fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
		pgIndexSchema(t,p,b)
		pgIndexControlSeed(run)
		pgIndexControlCheck(run)
		pgIndexPrivateRollback(t,p,b)
		pgIndexDiagnostics(run,true)
		if !b.PG { pgIndexNativeRoundtrip(t,p,b) }
	})
}
