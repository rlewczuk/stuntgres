package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Custom USAGE-only schema ACLs have PostgreSQL SQL setup but no native public
// mutation API. The matching native ACL/no-fallback unit uses real catalog ACLs;
// ordinary current_schemas, hidden denial and bounded array storage stay shared.
func TestReferenceSearchPathDomainArrayAndACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"path_domain_reader"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "hidden", "visible")
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			for _, q := range []string{"CREATE USER path_domain_reader", "CREATE SCHEMA hidden", "CREATE SCHEMA visible", "CREATE TABLE public.bounded(id pg_catalog._name)"} {
				run(fixture.Exec(q, 0))
			}
			if backend.PG {
				// Match the existing native bootstrap public-schema authority.
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO path_domain_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION path_domain_reader", 0))
			run(fixture.Exec("SET search_path TO hidden,missing,visible,visible,public", 0))
			baseline := fixture.Select("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{public}"}})
			run(baseline)
			run(fixture.Prepared(baseline))
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema hidden")
			want.Position = "16"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT id FROM hidden.t", want)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO public.bounded VALUES($1),(NULL)", 2, "{visible,public}")))
			stored := fixture.Select("SELECT id FROM public.bounded", []string{"id"}, []string{"_NAME"}, [][]any{{"{visible,public}"}, {nil}})
			run(stored)
			run(fixture.Prepared(stored))
			if backend.PG {
				// Explicit ACL oracle control, not a native SQL GRANT claim.
				run(fixture.Exec("GRANT USAGE ON SCHEMA visible TO path_domain_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION path_domain_reader", 0))
				visible := fixture.Select("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{visible,public}"}})
				run(visible)
				run(fixture.Prepared(visible))
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema visible")
				want.Position = "14"
				run(fixture.ErrorDiagnostic("CREATE TABLE no_fallback(id INT)", want))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
