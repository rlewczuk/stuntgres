package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedGeneratorDoesNotInvalidate(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			value := w.value
			shadow := w.shadow
			c := w.c
			s := shadow()
			value(s, 10)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT setval('b.s',40,true)", 40)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT currval('b.s')", 40)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT lastval()", 40)
			exec("SET search_path TO a,a,b")
			value(s, 41)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 100)
		})
	}
}
