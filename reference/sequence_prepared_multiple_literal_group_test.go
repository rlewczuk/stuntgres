package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedMultipleLiteralGroup(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s'),nextval('t')")
			for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.t CACHE 1"} {
				exec(q)
			}
			w.held(s, sequenceInt64Step(referenceStep{selectRows: true, columns: []string{"nextval", "nextval"}, types: []string{"INT8", "INT8"}, rows: [][]any{{int64(100), int64(200)}}}))
		})
	}
}
