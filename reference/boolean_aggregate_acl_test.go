package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceBooleanAggregateSourceACL(t *testing.T) {
	aggregateSourceACL(t, "boolean_reader", "BOOL", []string{"bool_and(b)", "every(b)", "bool_or(b)", "every(b)"})
}

func TestReferenceAnyValueSourceACL(t *testing.T) {
	aggregateSourceACL(t, "any_value_reader", "INT", []string{"any_value(b)", "any_value(b)", "any_value(b)", "any_value(b)"})
}

func aggregateSourceACL(t *testing.T, role, typ string, expressions []string) {
	t.Helper()
	roleReferenceCleanup(t, []string{role})
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE USER "+role, 0))
			run(referenceExec("CREATE TABLE source(b "+typ+")", 0))
			if backend.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
			}
			trustedRoutes := []bool{false}
			if !backend.pg && typ == "BOOL" {
				trustedRoutes = append(trustedRoutes, true)
			}
			for _, trusted := range trustedRoutes {
				reader := conn
				if trusted {
					connector, err := p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, role)
					if err != nil {
						t.Fatal(err)
					}
					pool := sql.OpenDB(connector)
					defer pool.Close()
					reader, err = pool.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
				} else {
					run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
				}
				referenceRunStep(t, p.ctx, reader, backend.recorder, referenceExec("CREATE TABLE target(b "+typ+")", 0))
				for i, q := range []string{"SELECT " + expressions[0] + " FROM source", "SELECT " + expressions[1] + " FROM source WHERE FALSE", "SELECT " + expressions[2] + " FROM source WHERE NULL", "INSERT INTO target SELECT " + expressions[3] + " FROM source WHERE FALSE"} {
					want := controlsReferenceDiagnostic("42501", "permission denied for table source")
					if typ == "BOOL" || i == 3 {
						result, err := reader.ExecContext(p.ctx, q)
						if result != nil {
							t.Fatal("denied Exec exposed result")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					if i < 3 {
						rows, err := reader.QueryContext(p.ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("denied Query exposed cursor")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					stmt, err := reader.PrepareContext(p.ctx, q)
					if err != nil {
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare exposed statement")
						}
						t.Fatal("semantic Prepare must succeed before Execute authorization", err)
					}
					result, err := stmt.ExecContext(p.ctx)
					if closeErr := stmt.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if result != nil {
						t.Fatal("denied prepared Exec exposed result")
					}
					referenceAssertDiagnostic(t, err, &want)
				}
				typeName := "BOOL"
				if typ == "INT" {
					typeName = "INT4"
				}
				if !trusted {
					run(referenceSelect("SELECT b FROM target", []string{"b"}, []string{typeName}, nil))
				} else {
					rows, err := reader.QueryContext(p.ctx, "SELECT b FROM target")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "b" {
						t.Fatal(columns, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typeName {
						t.Fatal(types, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("denied INSERT published data", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				}
				referenceRunStep(t, p.ctx, reader, backend.recorder, referenceExec("DROP TABLE target", 0))
				if !trusted {
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
