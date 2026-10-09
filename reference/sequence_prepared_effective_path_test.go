package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedEffectivePath(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 10", "CREATE SEQUENCE b.s START 100", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s')")
			value(s, 10)
			exec("SET search_path TO b,a")
			value(s, 100)
			exec("CREATE TEMP SEQUENCE s START 200")
			value(s, 200)
		})
	}
}
