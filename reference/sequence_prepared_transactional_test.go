package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedTransactional(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			failure := w.failure
			exec("CREATE SEQUENCE s")
			s := hold("SELECT nextval('s')")
			exec("BEGIN")
			exec("ALTER SEQUENCE s RENAME TO identity")
			failure(s, "42P01", `relation "s" does not exist`, "16")
			exec("ROLLBACK")
			value(s, 1)
			exec("BEGIN")
			exec("ALTER SEQUENCE s RESTART 100")
			value(s, 100)
			exec("ROLLBACK")
			value(s, 2)
			exec("BEGIN")
			exec("ALTER SEQUENCE s RENAME TO identity")
			exec("COMMIT")
			failure(s, "42P01", `relation "s" does not exist`, "16")
		})
	}
}
