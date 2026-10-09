package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

// The source's trusted DB consumer methods are independent of the recorded,
// pinned-Conn SQL-authentication and AnyValue companions.
func TestReferenceBooleanAggregateSourceConsumerRoutes(t *testing.T) {
	const role = "boolean_reader"
	roleReferenceCleanup(t, []string{role})
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, s := range []referenceStep{referenceExec("CREATE USER "+role, 0), referenceExec("CREATE TABLE source(b BOOL)", 0)} {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			var reader *sql.DB
			if backend.pg {
				s := referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0)
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				var err error
				reader, err = sql.Open("stuntgres_reference_postgres", referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				reader.SetMaxOpenConns(1)
				defer reader.Close()
				if _, err := reader.Exec("SET SESSION AUTHORIZATION " + role); err != nil {
					t.Fatal(err)
				}
			} else {
				connector, err := p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, role)
				if err != nil {
					t.Fatal(err)
				}
				reader = sql.OpenDB(connector)
				defer reader.Close()
			}
			s := referenceExec("CREATE TABLE target(b BOOL)", 0)
			s.nonContext = true
			referenceRunStep(t, p.ctx, reader, backend.recorder, s)
			for _, q := range []string{"SELECT bool_and(b) FROM source", "SELECT every(b) FROM source WHERE FALSE", "SELECT bool_or(b) FROM source WHERE NULL", "INSERT INTO target SELECT every(b) FROM source WHERE FALSE"} {
				want := controlsReferenceDiagnostic("42501", "permission denied for table source")
				result, err := reader.Exec(q)
				if result != nil {
					t.Fatal("denied Exec exposed result")
				}
				referenceAssertDiagnostic(t, err, &want)
				stmt, err := reader.Prepare(q)
				if err != nil {
					t.Fatal("semantic Prepare", err)
				}
				result, err = stmt.Exec()
				if closeErr := stmt.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if result != nil {
					t.Fatal("denied prepared Exec exposed result")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			empty := referenceSelect("SELECT b FROM target", []string{"b"}, []string{"BOOL"}, nil)
			empty.nonContext = true
			referenceRunStep(t, p.ctx, reader, backend.recorder, empty)
		})
	}
}
