package reference_test

import (
	"testing"
)

func TestReferenceSequencePreparedSchemaUsageInvalidation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		} // O: actual PostgreSQL authority/identity.
		t.Run(backend.name, func(t *testing.T) {
			w := newSequencePreparedSession(t, p, backend)
			exec := w.exec
			hold := w.hold
			value := w.value
			for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE b.s START 10", "GRANT USAGE ON SCHEMA b TO seq_path_reader", "GRANT USAGE ON SEQUENCE a.s,b.s TO seq_path_reader", "SET SESSION AUTHORIZATION seq_path_reader", "SET search_path TO a,b"} {
				exec(q)
			}
			s, qualified := hold("SELECT nextval('s')"), hold("SELECT nextval('b.s')")
			exec("RESET SESSION AUTHORIZATION")
			exec("GRANT USAGE ON SCHEMA a TO seq_path_reader")
			exec("SET SESSION AUTHORIZATION seq_path_reader")
			value(s, 100)
			exec("RESET SESSION AUTHORIZATION")
			exec("REVOKE USAGE ON SCHEMA b FROM seq_path_reader")
			exec("SET SESSION AUTHORIZATION seq_path_reader")
			var v int64
			err := qualified.QueryRowContext(p.ctx).Scan(&v)
			want := controlsReferenceDiagnostic("42501", "permission denied for schema b")
			want.Position = "16"
			referenceAssertDiagnostic(t, err, &want)
		})
	}
}
