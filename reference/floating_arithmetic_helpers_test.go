package reference_test

import "testing"

func referenceFloatingScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func referenceFloatingRun(t *testing.T, p *referencePair, b referenceBackend, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
}

func referenceFloatingError(t *testing.T, p *referencePair, b referenceBackend, q, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message})
		s.selectRows = true
		s.errorStage = "execute"
		s.prepared = prepared
		referenceFloatingRun(t, p, b, s)
	}
}
