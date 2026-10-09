package reference_test

import "testing"

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
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(referenceExec("CREATE TABLE target(b "+typ+")", 0))
			for i, q := range []string{"SELECT " + expressions[0] + " FROM source", "SELECT " + expressions[1] + " FROM source WHERE FALSE", "SELECT " + expressions[2] + " FROM source WHERE NULL", "INSERT INTO target SELECT " + expressions[3] + " FROM source WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					want := controlsReferenceDiagnostic("42501", "permission denied for table source")
					if prepared {
						stmt, err := conn.PrepareContext(p.ctx, q)
						if err != nil {
							if stmt != nil {
								stmt.Close()
								t.Fatal("failed Prepare exposed statement")
							}
							t.Fatal("semantic Prepare must succeed before Execute authorization", err)
						}
						_, err = stmt.ExecContext(p.ctx)
						stmt.Close()
						referenceAssertDiagnostic(t, err, &want)
					} else {
						s := referenceErrorDiagnostic(q, want)
						s.selectRows = i < 3
						s.errorStage = "execute"
						run(s)
					}
				}
			}
			typeName := "BOOL"
			if typ == "INT" {
				typeName = "INT4"
			}
			run(referenceSelect("SELECT b FROM target", []string{"b"}, []string{typeName}, nil))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}
