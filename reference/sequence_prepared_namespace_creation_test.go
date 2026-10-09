package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedNamespaceCreation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			for _, q := range []string{"CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s')")
			value(s, 10)
			exec("CREATE SCHEMA a")
			exec("CREATE SEQUENCE a.s START 100")
			value(s, 100)
			exec("DROP SCHEMA a CASCADE")
			value(s, 11)
		})
	}
}
