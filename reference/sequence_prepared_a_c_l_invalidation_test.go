package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedACLInvalidation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.name, func(t *testing.T) {
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
