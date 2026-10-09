package reference_test

import "testing"

// Custom USAGE-only schema ACLs have PostgreSQL SQL setup but no native public
// mutation API. The matching native ACL/no-fallback unit uses real catalog ACLs;
// ordinary current_schemas, hidden denial and bounded array storage stay shared.
func TestReferenceSearchPathDomainArrayAndACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"path_domain_reader"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "hidden", "visible")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) {
				t.Helper()
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			for _, q := range []string{"CREATE USER path_domain_reader", "CREATE SCHEMA hidden", "CREATE SCHEMA visible", "CREATE TABLE public.bounded(id pg_catalog._name)"} {
				run(referenceExec(q, 0))
			}
			if backend.pg {
				// Match the existing native bootstrap public-schema authority.
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO path_domain_reader", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION path_domain_reader", 0))
			run(referenceExec("SET search_path TO hidden,missing,visible,visible,public", 0))
			baseline := referenceSelect("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{public}"}})
			run(baseline)
			run(referencePrepared(baseline))
			want := controlsReferenceDiagnostic("42501", "permission denied for schema hidden")
			want.Position = "16"
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic("SELECT id FROM hidden.t", want)
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referencePrepared(referenceExec("INSERT INTO public.bounded VALUES($1),(NULL)", 2, "{visible,public}")))
			stored := referenceSelect("SELECT id FROM public.bounded", []string{"id"}, []string{"_NAME"}, [][]any{{"{visible,public}"}, {nil}})
			run(stored)
			run(referencePrepared(stored))
			if backend.pg {
				// Explicit ACL oracle control, not a native SQL GRANT claim.
				run(referenceExec("GRANT USAGE ON SCHEMA visible TO path_domain_reader", 0))
				run(referenceExec("SET SESSION AUTHORIZATION path_domain_reader", 0))
				visible := referenceSelect("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{visible,public}"}})
				run(visible)
				run(referencePrepared(visible))
				want := controlsReferenceDiagnostic("42501", "permission denied for schema visible")
				want.Position = "14"
				run(referenceErrorDiagnostic("CREATE TABLE no_fallback(id INT)", want))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
