package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequencePreparedRevokedMemory(t *testing.T) {
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
			c := w.c
			defer func() {
				exec("RESET SESSION AUTHORIZATION")
				exec("DROP SEQUENCE IF EXISTS public.no_shadow")
				exec("REVOKE USAGE ON SCHEMA public FROM seq_path_reader")
			}()
			for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SEQUENCE no_shadow CACHE 10", "GRANT USAGE ON SCHEMA public TO seq_path_reader", "GRANT ALL ON SEQUENCE no_shadow TO seq_path_reader", "SET SESSION AUTHORIZATION seq_path_reader"} {
				exec(q)
			}
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('no_shadow')", 1)
			statements := []*sql.Stmt{hold("SELECT nextval('no_shadow')"), hold("SELECT currval('no_shadow')"), hold("SELECT lastval()"), hold("SELECT setval('no_shadow',40)")}
			exec("RESET SESSION AUTHORIZATION")
			exec("REVOKE ALL ON SEQUENCE no_shadow FROM seq_path_reader")
			exec("SET SESSION AUTHORIZATION seq_path_reader")
			for _, s := range statements {
				var v int64
				err := s.QueryRowContext(p.ctx).Scan(&v)
				want := controlsReferenceDiagnostic("42501", "permission denied for sequence no_shadow")
				referenceAssertDiagnostic(t, err, &want)
			}
			exec("RESET SESSION AUTHORIZATION")
			exec("GRANT USAGE ON SEQUENCE no_shadow TO seq_path_reader")
			exec("SET SESSION AUTHORIZATION seq_path_reader")
			value(statements[0], 2)
			sequenceProbeValue(t, p.ctx, c, "SELECT currval('no_shadow')", 2)
			sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 2)
		})
	}
}
