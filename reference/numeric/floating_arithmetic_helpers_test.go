package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceFloatingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func referenceFloatingRun(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func referenceFloatingError(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
		s.SelectRows = true
		s.ErrorStage = "execute"
		s.Prepared = prepared
		referenceFloatingRun(t, p, b, s)
	}
}
