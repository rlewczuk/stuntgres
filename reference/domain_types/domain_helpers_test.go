package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Domain acceptance uses the ordinary dual-mode fixture and a pinned session.
func domainReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// Registered before borrowing: statements/connection close before schemas.
			t.Cleanup(func() {
				for _, schema := range []string{"domain_external", "domain_other", "domain_s"} {
					if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
				if _, err := backend.DB.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.one"); err != nil {
					t.Error(err)
				}
			})
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE SCHEMA domain_s", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE public.one (v integer)", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, backend, conn, recorder, &notices)
		})
	}
}
