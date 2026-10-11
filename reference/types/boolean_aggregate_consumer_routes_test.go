package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The source's trusted DB consumer methods are independent of the recorded,
// pinned-Conn SQL-authentication and AnyValue companions.
func TestReferenceBooleanAggregateSourceConsumerRoutes(t *testing.T) {
	const role = "boolean_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, s := range []fixture.Step{fixture.Exec("CREATE USER "+role, 0), fixture.Exec("CREATE TABLE source(b BOOL)", 0)} {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			var reader *sql.DB
			if backend.PG {
				s := fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0)
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				var err error
				reader, err = sql.Open("stuntgres_reference_postgres", fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				reader.SetMaxOpenConns(1)
				defer reader.Close()
				if _, err := reader.Exec("SET SESSION AUTHORIZATION " + role); err != nil {
					t.Fatal(err)
				}
			} else {
				connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, role)
				if err != nil {
					t.Fatal(err)
				}
				reader = sql.OpenDB(connector)
				defer reader.Close()
			}
			s := fixture.Exec("CREATE TABLE target(b BOOL)", 0)
			s.NonContext = true
			fixture.RunStep(t, p.Ctx, reader, backend.Recorder, s)
			for _, q := range []string{"SELECT bool_and(b) FROM source", "SELECT every(b) FROM source WHERE FALSE", "SELECT bool_or(b) FROM source WHERE NULL", "INSERT INTO target SELECT every(b) FROM source WHERE FALSE"} {
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table source")
				result, err := reader.Exec(q)
				if result != nil {
					t.Fatal("denied Exec exposed result")
				}
				fixture.AssertDiagnostic(t, err, &want)
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
				fixture.AssertDiagnostic(t, err, &want)
			}
			empty := fixture.Select("SELECT b FROM target", []string{"b"}, []string{"BOOL"}, nil)
			empty.NonContext = true
			fixture.RunStep(t, p.Ctx, reader, backend.Recorder, empty)
		})
	}
}
