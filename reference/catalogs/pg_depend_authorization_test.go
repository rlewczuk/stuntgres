package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependAuthorization(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_private")
	fixture.RoleReferenceCleanup(t, []string{"pgdep_reader"})
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE ROLE pgdep_reader", "CREATE SCHEMA pgdep_private", "CREATE TABLE pgdep_private.secret(a INT DEFAULT 1)", "COMMENT ON TABLE pgdep_private.secret IS 'not a dependency'")
		table := fixture.PgDependRelation(t, b, "pgdep_private.secret")
		ns := fixture.PgDependNamespace(t, b, table)
		d := fixture.PgDependDefinition(t, b, table, 1)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(context.Background(), "RESET ROLE")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pgdep_reader", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n")}, table)
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
			s = fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a")}, d)
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, conn, s)
		}
		failure := fixture.ErrorDiagnostic("DROP TABLE pgdep_private.secret", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema pgdep_private"))
		// Existing native utility lookup reports the schema-token position;
		// PostgreSQL18 reports no Position for this utility denial.
		postgres := *failure.Diagnostic
		failure.PostgresDiagnostic = &postgres
		failure.Diagnostic.Position = "12"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 1, "a"))
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	})
}
