package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgIndexesVisibilityScenario(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
	run := func(db fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0)) }
	for _, q := range []string{"CREATE ROLE pgi_reader", "CREATE ROLE pgi_owner", "CREATE SCHEMA pgi_private AUTHORIZATION pgi_owner", "CREATE TABLE public.pgi_denied(n INT)", "CREATE INDEX pgi_denied_index ON public.pgi_denied(n)", "SET SESSION AUTHORIZATION pgi_owner", "CREATE TABLE pgi_private.pgi_protected(n INT)", "CREATE INDEX pgi_private_index ON pgi_private.pgi_protected(n)", "RESET SESSION AUTHORIZATION"} {
		run(owner, q)
	}
	// PG fixture reset creates public without PUBLIC USAGE. Explicitly admit
	// schema lookup so this witness isolates denial of the parent table.
	if b.PG {
		run(owner, "GRANT USAGE ON SCHEMA public TO pgi_reader")
	}
	defer owner.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	defer reader.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	publicRow := fixture.PgIndexesRow("public", "pgi_denied", "pgi_denied_index", "CREATE INDEX pgi_denied_index ON public.pgi_denied USING btree (n)")
	privateRow := fixture.PgIndexesRow("pgi_private", "pgi_protected", "pgi_private_index", "CREATE INDEX pgi_private_index ON pgi_private.pgi_protected USING btree (n)")
	fixture.PgIndexesCheck(t, p, b, owner, "pgi_denied_index", publicRow)
	fixture.PgIndexesCheck(t, p, b, owner, "pgi_private_index", privateRow)
	stmt, err := reader.PrepareContext(p.Ctx, fixture.PgIndexesProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: fixture.PgIndexesProbe}
	run(reader, "SET SESSION AUTHORIZATION pgi_reader")
	for _, c := range []struct{ q, message, position string }{
		{"SELECT n FROM public.pgi_denied", "permission denied for table pgi_denied", ""},
		{"SELECT n FROM pgi_private.pgi_protected", "permission denied for schema pgi_private", "15"},
	} {
		s := fixture.ImplicitError(c.q, fixture.Diagnostic{Code: "42501", Message: c.message, Position: c.position})
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
	}
	for _, c := range []struct {
		name string
		row  []any
	}{{"pgi_denied_index", publicRow}, {"pgi_private_index", privateRow}} {
		want := c.row
		if !b.PG {
			want = nil
		}
		fixture.PgIndexesCheck(t, p, b, reader, c.name, want)
		fixture.PgIndexesCheck(t, p, b, held, c.name, want)
	}
	run(reader, "RESET SESSION AUTHORIZATION")
	fixture.PgIndexesCheck(t, p, b, held, "pgi_denied_index", publicRow)
	run(reader, "SET SESSION AUTHORIZATION pgi_owner")
	run(reader, "SET search_path TO public")
	fixture.PgIndexesCheck(t, p, b, reader, "pgi_private_index", privateRow)
	run(reader, "RESET SESSION AUTHORIZATION")
}
func TestReferencePGIndexesVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgi_reader", "pgi_owner"})
	fixture.SchemaReferenceCleanup(t, "pgi_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { pgIndexesVisibilityScenario(t, p, b) })
	}
}
