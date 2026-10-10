package reference_test

import "testing"

func pgIndexesVisibilityScenario(t *testing.T, p *referencePair, b referenceBackend) {
	owner, reader := referenceLockConnections(t, p.ctx, b.db)
	run := func(db referenceExecutor, q string) { referenceRunStep(t, p.ctx, db, b.recorder, referenceExec(q, 0)) }
	for _, q := range []string{"CREATE ROLE pgi_reader", "CREATE ROLE pgi_owner", "CREATE SCHEMA pgi_private AUTHORIZATION pgi_owner", "CREATE TABLE public.pgi_denied(n INT)", "CREATE INDEX pgi_denied_index ON public.pgi_denied(n)", "SET SESSION AUTHORIZATION pgi_owner", "CREATE TABLE pgi_private.pgi_protected(n INT)", "CREATE INDEX pgi_private_index ON pgi_private.pgi_protected(n)", "RESET SESSION AUTHORIZATION"} {
		run(owner, q)
	}
	// PG fixture reset creates public without PUBLIC USAGE. Explicitly admit
	// schema lookup so this witness isolates denial of the parent table.
	if b.pg {
		run(owner, "GRANT USAGE ON SCHEMA public TO pgi_reader")
	}
	defer owner.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	defer reader.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	publicRow := pgIndexesRow("public", "pgi_denied", "pgi_denied_index", "CREATE INDEX pgi_denied_index ON public.pgi_denied USING btree (n)")
	privateRow := pgIndexesRow("pgi_private", "pgi_protected", "pgi_private_index", "CREATE INDEX pgi_private_index ON pgi_private.pgi_protected USING btree (n)")
	pgIndexesCheck(t, p, b, owner, "pgi_denied_index", publicRow)
	pgIndexesCheck(t, p, b, owner, "pgi_private_index", privateRow)
	stmt, err := reader.PrepareContext(p.ctx, pgIndexesProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: reader, statement: stmt, query: pgIndexesProbe}
	run(reader, "SET SESSION AUTHORIZATION pgi_reader")
	for _, c := range []struct{ q, message, position string }{
		{"SELECT n FROM public.pgi_denied", "permission denied for table pgi_denied", ""},
		{"SELECT n FROM pgi_private.pgi_protected", "permission denied for schema pgi_private", "15"},
	} {
		s := referenceImplicitError(c.q, referenceDiagnostic{Code: "42501", Message: c.message, Position: c.position})
		s.selectRows = true
		referenceRunStep(t, p.ctx, reader, b.recorder, s)
	}
	for _, c := range []struct {
		name string
		row  []any
	}{{"pgi_denied_index", publicRow}, {"pgi_private_index", privateRow}} {
		want := c.row
		if !b.pg {
			want = nil
		}
		pgIndexesCheck(t, p, b, reader, c.name, want)
		pgIndexesCheck(t, p, b, held, c.name, want)
	}
	run(reader, "RESET SESSION AUTHORIZATION")
	pgIndexesCheck(t, p, b, held, "pgi_denied_index", publicRow)
	run(reader, "SET SESSION AUTHORIZATION pgi_owner")
	run(reader, "SET search_path TO public")
	pgIndexesCheck(t, p, b, reader, "pgi_private_index", privateRow)
	run(reader, "RESET SESSION AUTHORIZATION")
}
func TestReferencePGIndexesVisibility(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgi_reader", "pgi_owner"})
	schemaReferenceCleanup(t, "pgi_private")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) { pgIndexesVisibilityScenario(t, p, b) })
	}
}
