package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedOwnerTransition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if !backend.PG {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SCHEMA a", "CREATE SCHEMA b", "GRANT CREATE ON SCHEMA b TO seq_path_reader", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
				exec(q)
			}
			s := hold("SELECT nextval('s')")
			exec("CREATE SEQUENCE a.s START 100")
			exec("ALTER SEQUENCE b.s OWNER TO seq_path_reader")
			value(s, 100)
		})
	}
}
