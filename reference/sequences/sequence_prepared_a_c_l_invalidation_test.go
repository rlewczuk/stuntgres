package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedACLInvalidation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			value := w.value
			shadow := w.shadow
			s := shadow()
			exec("GRANT USAGE ON SEQUENCE b.s TO PUBLIC")
			value(s, 100)
		})
	}
}
