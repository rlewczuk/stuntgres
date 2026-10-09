package reference_test

import (
	"testing"
)

func referenceCheckBackends(t *testing.T, scenario func(*testing.T, func(referenceStep))) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			scenario(t, func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) })
		})
	}
}
