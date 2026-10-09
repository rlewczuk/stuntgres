package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedGeneratorDoesNotInvalidate(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			value := w.value
			shadow := w.shadow
			c := w.c
			s := shadow()
			value(s, 10)
			sequenceProbeValue(t, p.ctx, c, "SELECT setval('b.s',40,true)", 40)
			sequenceProbeValue(t, p.ctx, c, "SELECT currval('b.s')", 40)
			sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 40)
			exec("SET search_path TO a,a,b")
			value(s, 41)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 100)
		})
	}
}
