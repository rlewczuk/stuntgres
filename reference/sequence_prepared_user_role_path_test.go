package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedUserRolePath(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			for _, q := range []string{"CREATE USER seq_path_reader", "CREATE USER seq_path_reader2", "CREATE SCHEMA seq_path_reader AUTHORIZATION seq_path_reader", "CREATE SCHEMA seq_path_reader2 AUTHORIZATION seq_path_reader2", "SET SESSION AUTHORIZATION seq_path_reader", "CREATE SEQUENCE seq_path_reader.s START 30", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION seq_path_reader2", "CREATE SEQUENCE seq_path_reader2.s START 300", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION seq_path_reader", `SET search_path TO "$user",public`} {
				exec(q)
			}
			s := hold("SELECT nextval('s')")
			value(s, 30)
			exec("RESET SESSION AUTHORIZATION")
			exec("SET SESSION AUTHORIZATION seq_path_reader2")
			value(s, 300)
		})
	}
}
