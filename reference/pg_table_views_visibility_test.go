package reference_test

import "testing"

func TestReferencePGTableViewsVisibility(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgtv_reader", "pgtv_private_owner"})
	schemaReferenceCleanup(t, "pgtv_private", "pgtv_offpath")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			owner, reader := referenceLockConnections(t, p.ctx, b.db)
			run := func(db referenceExecutor, q string) { referenceRunStep(t, p.ctx, db, b.recorder, referenceExec(q, 0)) }
			defer owner.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			defer reader.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"CREATE ROLE pgtv_reader", "CREATE ROLE pgtv_private_owner", "CREATE SCHEMA pgtv_private AUTHORIZATION pgtv_private_owner", "CREATE SCHEMA pgtv_offpath AUTHORIZATION pgtv_private_owner", "CREATE TABLE public.pgtv_disclosed(n INT)", "CREATE VIEW public.pgtv_disclosed_view AS SELECT n FROM public.pgtv_disclosed", "SET SESSION AUTHORIZATION pgtv_private_owner", "CREATE TABLE pgtv_private.pgtv_disclosed(n INT)", "CREATE VIEW pgtv_private.pgtv_disclosed_view AS SELECT n FROM pgtv_private.pgtv_disclosed", "CREATE TABLE pgtv_offpath.pgtv_disclosed(n INT)", "CREATE VIEW pgtv_offpath.pgtv_disclosed_view AS SELECT n FROM pgtv_offpath.pgtv_disclosed", "RESET SESSION AUTHORIZATION"} {
				run(owner, q)
			}
			if b.pg {
				run(owner, "GRANT USAGE ON SCHEMA public TO pgtv_reader")
			}
			query := "SELECT * FROM pg_catalog.pg_tables WHERE tablename='pgtv_disclosed' ORDER BY schemaname"
			heldStmt, err := reader.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer heldStmt.Close()
			tableHeld := referenceHeldStatement{referenceExecutor: reader, statement: heldStmt, query: query}
			viewQuery := "SELECT schemaname,viewname,viewowner FROM pg_catalog.pg_views WHERE viewname='pgtv_disclosed_view' ORDER BY schemaname"
			viewStmt, err := reader.PrepareContext(p.ctx, viewQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer viewStmt.Close()
			viewHeld := referenceHeldStatement{referenceExecutor: reader, statement: viewStmt, query: viewQuery}
			expected := [][]any{pgTableRow("pgtv_offpath", "pgtv_disclosed", "pgtv_private_owner", false), pgTableRow("pgtv_private", "pgtv_disclosed", "pgtv_private_owner", false), pgTableRow("public", "pgtv_disclosed", pgTableViewsOwner(b), false)}
			check := func(tableDB, viewDB referenceExecutor) {
				referenceRunStep(t, p.ctx, tableDB, b.recorder, pgTableViewSelect(b, query, pgTableColumns, pgTableTypes, expected))
				referenceRunStep(t, p.ctx, viewDB, b.recorder, referenceSelect(viewQuery, pgViewColumns[:3], pgViewTypes[:3], [][]any{{"pgtv_offpath", "pgtv_disclosed_view", "pgtv_private_owner"}, {"pgtv_private", "pgtv_disclosed_view", "pgtv_private_owner"}, {"public", "pgtv_disclosed_view", pgTableViewsOwner(b)}}))
			}
			check(owner, owner)
			run(reader, "SET SESSION AUTHORIZATION pgtv_reader")
			run(reader, "SET search_path TO public")
			for _, c := range []struct{ q, message, position string }{{"SELECT n FROM public.pgtv_disclosed", "permission denied for table pgtv_disclosed", ""}, {"SELECT n FROM pgtv_private.pgtv_disclosed", "permission denied for schema pgtv_private", "15"}} {
				s := referenceImplicitError(c.q, referenceDiagnostic{Code: "42501", Message: c.message, Position: c.position})
				s.selectRows = true
				referenceRunStep(t, p.ctx, reader, b.recorder, s)
			}
			check(reader, reader)
			check(tableHeld, viewHeld)
				run(owner, "GRANT SELECT(n) ON public.pgtv_disclosed TO pgtv_reader")
				referenceRunStep(t, p.ctx, reader, b.recorder, referenceSelect("SELECT n FROM public.pgtv_disclosed", []string{"n"}, []string{"INT4"}, nil))
				check(reader, reader)
				check(tableHeld, viewHeld)
				run(owner, "REVOKE SELECT(n) ON public.pgtv_disclosed FROM pgtv_reader")
				s := referenceImplicitError("SELECT n FROM public.pgtv_disclosed", referenceDiagnostic{Code: "42501", Message: "permission denied for table pgtv_disclosed"})
				s.selectRows = true
				referenceRunStep(t, p.ctx, reader, b.recorder, s)
				check(reader, reader)
				check(tableHeld, viewHeld)
			pgViewSummary(t, p, b, reader, "pgtv_private", "pgtv_disclosed_view", "pgtv_private_owner", "pgtv_private", "pgtv_disclosed")
			run(reader, "RESET SESSION AUTHORIZATION")
			run(reader, "SET SESSION AUTHORIZATION pgtv_private_owner")
			run(reader, "SET search_path TO pgtv_offpath")
			check(reader, reader)
			check(tableHeld, viewHeld)
			run(reader, "RESET SESSION AUTHORIZATION")
		})
	}
}
