package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedDropRecreate(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			failure := w.failure
			c := w.c
			exec("CREATE SEQUENCE s START 10")
			next, curr := hold("SELECT nextval('s')"), hold("SELECT currval('s')")
			exec("DROP SEQUENCE s")
			failure(next, "42P01", `relation "s" does not exist`, "16")
			failure(curr, "42P01", `relation "s" does not exist`, "16")
			exec("CREATE SEQUENCE s START 90")
			failure(curr, "55000", `currval of sequence "s" is not yet defined in this session`, "")
			value(next, 90)
			value(curr, 90)
			sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 90)
		})
	}
}
