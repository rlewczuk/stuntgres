package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedSchemaMove(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			failure := w.failure
			exec("CREATE SCHEMA moved")
			exec("SET search_path TO public")
			exec("CREATE SEQUENCE s START 20")
			s, qualified := hold("SELECT nextval('s')"), hold("SELECT nextval('public.s')")
			exec("ALTER SEQUENCE s SET SCHEMA moved")
			failure(s, "42P01", `relation "s" does not exist`, "16")
			failure(qualified, "42P01", `relation "public.s" does not exist`, "16")
			exec("SET search_path TO moved,public")
			value(s, 20)
			exec("CREATE SEQUENCE public.s START 70")
			value(qualified, 70)
		})
	}
}
