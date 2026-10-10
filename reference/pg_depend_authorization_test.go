package reference_test

import (
	"context"
	"testing"
)

func TestReferencePGDependAuthorization(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_private")
	roleReferenceCleanup(t, []string{"pgdep_reader"})
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE ROLE pgdep_reader", "CREATE SCHEMA pgdep_private", "CREATE TABLE pgdep_private.secret(a INT DEFAULT 1)", "COMMENT ON TABLE pgdep_private.secret IS 'not a dependency'")
		table := pgDependRelation(t, b, "pgdep_private.secret")
		ns := pgDependNamespace(t, b, table)
		d := pgDependDefinition(t, b, table, 1)
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(context.Background(), "RESET ROLE")
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET ROLE pgdep_reader", 0))
		for _, prepared := range []bool{false, true} {
			s := pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", [][]any{pgDependEdge(1259, table, 0, 2615, ns, 0, "n")}, table)
			s.prepared = prepared
			pgDescriptionExecutorRows(t, p, b, conn, s)
			s = pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{pgDependEdge(2604, d, 0, 1259, table, 1, "a")}, d)
			s.prepared = prepared
			pgDescriptionExecutorRows(t, p, b, conn, s)
		}
		failure := referenceErrorDiagnostic("DROP TABLE pgdep_private.secret", controlsReferenceDiagnostic("42501", "permission denied for schema pgdep_private"))
		// Existing native utility lookup reports the schema-token position;
		// PostgreSQL18 reports no Position for this utility denial.
		postgres := *failure.diagnostic
		failure.postgresDiagnostic = &postgres
		failure.diagnostic.Position = "12"
		referenceRunStep(t, p.ctx, conn, b.recorder, failure)
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("RESET ROLE", 0))
		pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 1, "a"))
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	})
}
