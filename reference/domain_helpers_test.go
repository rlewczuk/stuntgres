package reference_test

import (
	"database/sql"
	"testing"
)

// Domain acceptance uses the ordinary dual-mode fixture and a pinned session.
func domainReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn, *referenceRecorder, *[]referenceDiagnostic)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			// Registered before borrowing: statements/connection close before schemas.
			t.Cleanup(func() {
				for _, schema := range []string{"domain_external", "domain_other", "domain_s"} {
					if _, err := backend.db.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
				if _, err := backend.db.ExecContext(p.ctx, "DROP TABLE IF EXISTS public.one"); err != nil {
					t.Error(err)
				}
			})
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, backend.pg, &notices)
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE SCHEMA domain_s", 0))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE public.one (v integer)", 0))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, backend, conn, recorder, &notices)
		})
	}
}
